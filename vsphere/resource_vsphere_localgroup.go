package vsphere

import (
	"context"
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-provider-vsphere/vsphere/internal/helper/customattribute"
	"github.com/vmware/govmomi/ssoadmin"
	"github.com/vmware/govmomi/ssoadmin/types"
	//	"github.com/vmware/govmomi/vim25"
	//	"github.com/vmware/govmomi/vim25/soap"
)

func resourceVSphereLocalgroup() *schema.Resource {
	return &schema.Resource{
		Create: resourceVSphereLocalgroupCreate,
		Read:   resourceVSphereLocalgroupRead,
		Update: resourceVSphereLocalgroupRead,
		Delete: resourceVSphereLocalgroupRead,
		/*Importer: &schema.ResourceImporter{
			State: resourceVSphereLocalgroupRead,
		},*/

		SchemaVersion: 1,
		//	MigrateState:  resourceVSphereLocalgroupMigrateState,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:        schema.TypeString,
				Description: "The name of the Local Group.",
				Required:    true,
				/*StateFunc:    Localgroup.NormalizePath,
				ValidateFunc: validation.NoZeroValues,*/
			},
			"details": {
				Type:        schema.TypeString,
				Description: "The details of the Local Group.",
				Required:    true,
				/*StateFunc:    Localgroup.NormalizePath,
				ValidateFunc: validation.NoZeroValues,*/
			},
			// Tagging
			vSphereTagAttributeKey: tagsSchema(),
			// Custom Attributes
			customattribute.ConfigKey: customattribute.ConfigSchema(),
		},
	}
}

func resourceVSphereLocalgroupCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).ssoAdminClient
	ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	defer cancel()

	/*zestfullyc, err := vim25.NewClient(ctx, soap.NewClient(meta.(*Client).vimClient.URL(), true))


	println("made it this far")
	ssoadminClient, err := ssoadmin.NewClient(ctx, client.Client)
	if err != nil {
		return fmt.Errorf("error creating ssoadmin client %s", err)
	}
	println("trying to log in....")
	err := client.Login(ctx)
	if err != nil {
		return fmt.Errorf("error logging in >>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>> %s", err)
	}
	println("We're logged in")*/

	err := client.CreateGroup(ctx, d.Get("name").(string), types.AdminGroupDetails{Description: d.Get("details").(string)})

	if err != nil {
		return fmt.Errorf("error creating local group: %s", err)
	}

	thisGroup, err := client.FindGroup(ctx, d.Get("name").(string))
	println(thisGroup)

	d.SetId(thisGroup.Id.Name)

	return resourceVSphereLocalgroupRead(d, meta)
}

func resourceVSphereLocalgroupRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).vimClient
	ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	defer cancel()

	ssoadminClient, err := ssoadmin.NewClient(ctx, client.Client)
	if err != nil {
		return fmt.Errorf("Error reading Local group: %s", err)
	}
	if thisGroup, err := ssoadminClient.FindGroup(ctx, d.Get("name").(string)); err != nil {
		log.Println("[INFO] Setting the values")
		_ = d.Set("name", thisGroup.Id.Name)
		_ = d.Set("details", thisGroup.Details)
	}
	return nil
}

/* func resourceVSphereLocalgroupUpdate(d *schema.ResourceData, meta interface{}) error {
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
			task, err := newpa.MoveInto(ctx, []vim.ManagedObjectReference{fo.Reference()})
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
} */
