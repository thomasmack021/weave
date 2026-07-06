# coexistence_base.tf
# Intentionally messy formatting to prove unrelated content survives untouched.

locals {
  region = "europe-west1" # default region
  labels = {
    team = "platform"
    env  = "dev"
  }
}


/*
 * A pre-existing resource that must remain byte-for-byte unchanged.
 */
resource "google_storage_bucket" "state" {
  name     = "acme-tfstate"
  location = "EU"

  versioning {
    enabled = true # keep history
  }
}

module "api" {
  source = "git::https://github.com/acme/iac-modules.git//modules/cloud-run?ref=v2.4.0"

  name  = var.service_name
  image = var.container_image
}
