# =============================================================================
# Containarium Terraform Module — Main
# =============================================================================
# Static IP, firewall rules, regular (non-spot) VM instance.
# Network references are parameterized for both default and VPC networks.

terraform {
  # >= 1.2 for the lifecycle precondition guarding sentinel_auth_secret
  # when enable_peer_mtls is set (see sentinel.tf, issue #341).
  required_version = ">= 1.2"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

# -----------------------------------------------------------------------------
# Locals
# -----------------------------------------------------------------------------

locals {
  network    = var.network_self_link != "" ? var.network_self_link : "default"
  subnetwork = var.subnetwork_self_link != "" ? var.subnetwork_self_link : null
}

# -----------------------------------------------------------------------------
# Static External IP for the jump server
# -----------------------------------------------------------------------------

resource "google_compute_address" "jump_server_ip" {
  name    = "${var.instance_name}-ip"
  region  = var.region
  project = var.project_id
}

# -----------------------------------------------------------------------------
# Firewall Rules
# -----------------------------------------------------------------------------

# Operator SSH to jump server. Phase 2.3: sources now from
