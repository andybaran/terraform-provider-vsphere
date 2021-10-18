package vsphere

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/terraform-provider-vsphere/vsphere/internal/helper/Localgroup"
	"github.com/hashicorp/terraform-provider-vsphere/vsphere/internal/helper/customattribute"
	"github.com/hashicorp/terraform-provider-vsphere/vsphere/internal/helper/viapi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/ssoadmin"
	"github.com/vmware/govmomi/vim25/types"
)

func resourceVSphereLocalgroup() *schema.Resource {
	return &schema.Resource{
		Create: resourceVSphereLocalgroupCreate,
		Read:   resourceVSphereLocalgroupRead,
		Update: resourceVSphereLocalgroupUpdate,
		Delete: resourceVSphereLocalgroupDelete,
		Importer: &schema.ResourceImporter{
			State: resourceVSphereLocalgroupImport,
		},

		SchemaVersion: 1,
		MigrateState:  resourceVSphereLocalgroupMigrateState,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:         schema.TypeString,
				Description:  "The name of the Local Group.",
				Required:     true,
				StateFunc:    Localgroup.NormalizePath,
				ValidateFunc: validation.NoZeroValues,
			},
			"details": {
				Type:         schema.TypeString,
				Description:  "The details of the Local Group.",
				Required:     true,
				StateFunc:    Localgroup.NormalizePath,
				ValidateFunc: validation.NoZeroValues,
			},
			// Tagging
			vSphereTagAttributeKey: tagsSchema(),
			// Custom Attributes
			customattribute.ConfigKey: customattribute.ConfigSchema(),
		},
	}
}

func resourceVSphereLocalgroupCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).vimClient
	tagsClient, err := tagsManagerIfDefined(d, meta)
	if err != nil {
		return err
	}
	// Verify a proper vCenter before proceeding if custom attributes are defined
	attrsProcessor, err := customattribute.GetDiffProcessorIfAttributesDefined(client, d)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	defer cancel()

	ssoadminClient := ssoadmin.NewClient(client)

	err = ssoadminClient.CreateGroup(ctx, d.Get("name").(string), d.Get("details").(string))

	if err != nil {
		return fmt.Errorf("error creating Local group: %s", err)
	}

	thisGroup := ssoadminClient.FindGroup(d.Get("name").(string))
	d.SetId(thisGroup.Reference().Value)

	// Apply any pending tags now
	if tagsClient != nil {
		if err := processTagDiff(tagsClient, d, thisGroup); err != nil {
			return fmt.Errorf("error updating tags: %s", err)
		}
	}

	// Set custom attributes
	if attrsProcessor != nil {
		if err := attrsProcessor.ProcessDiff(thisGroup); err != nil {
			return fmt.Errorf("error setting custom attributes: %s", err)
		}
	}

	return resourceVSphereLocalgroupRead(d, meta)
}

func resourceVSphereLocalgroupRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).vimClient
	fo, err := Localgroup.FromID(client, d.Id())
	if err != nil {
		return fmt.Errorf("cannot locate Localgroup: %s", err)
	}

	// Determine the Localgroup type first. We use the Localgroup as the source of truth
	// here versus the state so that we can support import.
	ft, err := Localgroup.FindType(fo)
	if err != nil {
		return fmt.Errorf("cannot determine Localgroup type: %s", err)
	}

	// Again, to support a clean import (which is done off of absolute path to
	// the Localgroup), we discover the datacenter from the path (if it's a thing).
	var dc *object.Datacenter
	p := fo.InventoryPath
	if ft != Localgroup.VSphereLocalgroupTypeDatacenter {
		particle := Localgroup.RootPathParticle(ft)
		dcp, err := particle.SplitDatacenter(p)
		if err != nil {
			return fmt.Errorf("cannot determine datacenter path: %s", err)
		}
		dc, err = getDatacenter(client, dcp)
		if err != nil {
			return fmt.Errorf("cannot find datacenter from path %q: %s", dcp, err)
		}
		relative, err := particle.SplitRelative(p)
		if err != nil {
			return fmt.Errorf("cannot determine relative Localgroup path: %s", err)
		}
		p = relative
	}

	_ = d.Set("path", Localgroup.NormalizePath(p))
	_ = d.Set("type", ft)
	if dc != nil {
		_ = d.Set("datacenter_id", dc.Reference().Value)
	}

	// Read tags if we have the ability to do so
	if tagsClient, _ := meta.(*Client).TagsManager(); tagsClient != nil {
		if err := readTagsForResource(tagsClient, fo, d); err != nil {
			return fmt.Errorf("error reading tags: %s", err)
		}
	}

	// Read custom attributes
	if customattribute.IsSupported(client) {
		moLocalgroup, err := Localgroup.Properties(fo)
		if err != nil {
			return err
		}
		customattribute.ReadFromResource(moLocalgroup.Entity(), d)
	}

	return nil
}

func resourceVSphereLocalgroupUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).vimClient
	tagsClient, err := tagsManagerIfDefined(d, meta)
	if err != nil {
		return err
	}
	// Verify a proper vCenter before proceeding if custom attributes are defined
	attrsProcessor, err := customattribute.GetDiffProcessorIfAttributesDefined(client, d)
	if err != nil {
		return err
	}

	fo, err := Localgroup.FromID(client, d.Id())
	if err != nil {
		return fmt.Errorf("cannot locate Localgroup: %s", err)
	}

	// Apply any pending tags first as it's the lesser expensive of the two
	// operations
	if tagsClient != nil {
		if err := processTagDiff(tagsClient, d, fo); err != nil {
			return fmt.Errorf("error updating tags: %s", err)
		}
	}

	if attrsProcessor != nil {
		if err := attrsProcessor.ProcessDiff(fo); err != nil {
			return fmt.Errorf("error setting custom attributes: %s", err)
		}
	}

	var dc *object.Datacenter
	if dcID, ok := d.GetOk("datacenter_id"); ok {
		var err error
		dc, err = datacenterFromID(client, dcID.(string))
		if err != nil {
			return fmt.Errorf("cannot locate datacenter: %s", err)
		}
	}

	if d.HasChange("path") {
		// The path has changed, which could mean either a change in parent, a
		// change in name, or both.
		ft := Localgroup.VSphereLocalgroupType(d.Get("type").(string))
		oldp, newp := d.GetChange("path")
		oldpa, err := Localgroup.ParentFromPath(client, oldp.(string), ft, dc)
		if err != nil {
			return fmt.Errorf("error parsing parent Localgroup from path %q: %s", oldp.(string), err)
		}
		newpa, err := Localgroup.ParentFromPath(client, newp.(string), ft, dc)
		if err != nil {
			return fmt.Errorf("error parsing parent Localgroup from path %q: %s", newp.(string), err)
		}
		oldn := path.Base(oldp.(string))
		newn := path.Base(newp.(string))

		if oldn != newn {
			// Localgroup base name has changed and needs a rename
			if err := viapi.RenameObject(client, fo.Reference(), newn); err != nil {
				return fmt.Errorf("could not rename Localgroup: %s", err)
			}
		}
		if oldpa.Reference().Value != newpa.Reference().Value {
			// The parent Localgroup has changed - we need to move the Localgroup into the
			// new path
			ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
			defer cancel()
			task, err := newpa.MoveInto(ctx, []types.ManagedObjectReference{fo.Reference()})
			if err != nil {
				return fmt.Errorf("could not move Localgroup: %s", err)
			}
			tctx, tcancel := context.WithTimeout(context.Background(), defaultAPITimeout)
			defer tcancel()
			if err := task.Wait(tctx); err != nil {
				return fmt.Errorf("error on waiting for move task completion: %s", err)
			}
		}
	}

	return resourceVSphereLocalgroupRead(d, meta)
}

func resourceVSphereLocalgroupDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).vimClient
	fo, err := Localgroup.FromID(client, d.Id())
	if err != nil {
		return fmt.Errorf("cannot locate Localgroup: %s", err)
	}

	// We don't destroy if the Localgroup has children. This might be flaggable in
	// the future, but I don't think it's necessary at this point in time -
	// better to have hardcoded safe behavior than hardcoded unsafe behavior.
	ne, err := Localgroup.HasChildren(fo)
	if err != nil {
		return fmt.Errorf("error checking for Localgroup contents: %s", err)
	}
	if ne {
		return errors.New("Localgroup is not empty, please remove all items before deleting")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	defer cancel()
	task, err := fo.Destroy(ctx)
	if err != nil {
		return fmt.Errorf("cannot delete Localgroup: %s", err)
	}
	tctx, tcancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	defer tcancel()
	if err := task.Wait(tctx); err != nil {
		return fmt.Errorf("error on waiting for deletion task completion: %s", err)
	}

	return nil
}

func resourceVSphereLocalgroupImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	// Our subject is the full path to a specific targetLocalgroup, for which we just get
	// the MOID for and then pass off to Read. Easy peasy.
	p := d.Id()
	if !strings.HasPrefix(p, "/") {
		return nil, errors.New("path must start with a trailing slash")
	}
	client := meta.(*Client).vimClient
	p = Localgroup.NormalizePath(p)
	targetLocalgroup, err := Localgroup.FromAbsolutePath(client, p)
	if err != nil {
		return nil, err
	}
	d.SetId(targetLocalgroup.Reference().Value)
	return []*schema.ResourceData{d}, nil
}
