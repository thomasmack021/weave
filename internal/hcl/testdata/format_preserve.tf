# format_preserve.tf
#
# Exercises hclwrite's lossless round-trip: comments, blank lines, and block
# structure must survive a Load -> Bytes cycle byte-for-byte.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    google = {
      source  = "hashicorp/google" # pinned by the landing zone
      version = "~> 5.0"
    }
  }
}

/*
 * Cloud Run service module.
 * Sourced from the central IaC library and pinned to a tag.
 */
module "api" {
  source = "git::https://github.com/acme/iac-modules.git//modules/cloud-run?ref=v2.4.0"

  name  = var.service_name # DNS-1123 service name
  image = var.container_image

  # Scaling configuration
  min_instances = 0
  max_instances = 10

  env = {
    LOG_LEVEL = "info"
    REGION    = "europe-west1"
  }
}
