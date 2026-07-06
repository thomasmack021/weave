terraform {
  required_version = ">= 1.5.0"
}

module "network" {
  source = "git::https://github.com/acme/iac-modules.git//modules/network?ref=v2.4.0"

  name = var.network_name
}
