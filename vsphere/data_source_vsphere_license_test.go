package vsphere

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
)

var testAccDataSourceVSphereLicenseExpectedRegexp = regexp.MustCompile("^group-v")

func TestAccDataSourceVSphereLicense_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			RunSweepers()
			testAccPreCheck(t)
			testAccDataSourceVSphereLicensePreCheck(t)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceVSphereLicenseConfig(),
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr(
						"data.vsphere_license.license_key",
						"id",
						testAccDataSourceVSphereFolderExpectedRegexp,
					),
				),
			},
		},
	})
}

func testAccDataSourceVSphereLicensePreCheck(t *testing.T) {
	if os.Getenv("TF_VAR_VSPHERE_DATACENTER") == "" {
		t.Skip("set TF_VAR_VSPHERE_DATACENTER to run vsphere_folder acceptance tests")
	}
}

func testAccDataSourceVSphereLicenseConfig() string {
	return fmt.Sprintf(`
data "vsphere_datacenter" "dc" {
  name = "%s"
}

resource "vsphere_license "license" {
	license_key = "0060Q-L910P-18KGD-0T8KH-0TX0M"
}

data "vsphere_license" "license" {
  license_key = "0060Q-L910P-18KGD-0T8KH-0TX0M"
}
`, os.Getenv("TF_VAR_VSPHERE_DATACENTER"))
}
