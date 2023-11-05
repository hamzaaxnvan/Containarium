# =============================================================================
# Example: Production Consumer (kafeido-infra style)
# =============================================================================
# This example shows how a production deployment (e.g., kafeido-infra) would
# consume the containarium module with VPC networking.
#
# Copy and adapt this for your production environment.

terraform {
  # backend "gcs" {
  #   bucket = "your-terraform-states-bucket"
  #   prefix = "containarium/production"
  # }
}

provider "google" {
  project = var.project_id
  region  = var.region
  zone    = var.zone
}

data "google_compute_network" "vpc" {
  name    = var.network_name
  project = var.project_id
}

data "google_compute_subnetwork" "subnet" {
  name    = var.subnetwork_name
  region  = var.region
  project = var.project_id
}

module "containarium" {
  # Pin to a specific release tag for production stability
  # source = "git::https://github.com/FootprintAI/Containarium//terraform/modules/containarium?ref=v0.8.2"

  # For local development/testing:
  source = "../../"

  # Project & region
  project_id    = var.project_id
  region        = var.region
  zone          = var.zone
