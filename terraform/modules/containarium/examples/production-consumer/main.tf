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
  instance_name = var.instance_name
  machine_type  = var.machine_type

  # VPC networking
  network_self_link    = data.google_compute_network.vpc.self_link
  subnetwork_self_link = data.google_compute_subnetwork.subnet.self_link
  spot_vm_external_ip  = false # Cloud NAT only

  # Production features
  enable_iap_firewall     = true
  jwt_secret              = var.jwt_secret
  fail2ban_whitelist_cidr = "10.0.0.0/8"
  instance_tags           = ["containarium-jump-server-usw1", "containarium-sentinel"]

  # Standard config
  admin_ssh_keys          = var.admin_ssh_keys
  allowed_ssh_sources     = var.allowed_ssh_sources
  use_spot_instance       = var.use_spot_instance
  use_persistent_disk     = var.use_persistent_disk
  enable_sentinel         = var.enable_sentinel
  containarium_binary_url = var.containarium_binary_url
  data_disk_size          = var.data_disk_size
  data_disk_type          = var.data_disk_type
  enable_disk_snapshots   = var.enable_disk_snapshots

  # Labels
  labels = var.labels
}

# Variables needed for this consumer
variable "project_id" {
  type = string
}

variable "region" {
  type    = string
  default = "us-west1"
}

variable "zone" {
  type    = string
  default = "us-west1-a"
}
