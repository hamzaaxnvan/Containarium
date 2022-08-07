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
# `allowed_management_sources` (defaults to VPC-only) rather than
# `allowed_ssh_sources` (which still defaults to 0.0.0.0/0 for
# user-facing services on the sentinel). Operators reach the
# jump server via VPN / IAP / bastion, not by exposing :22 to
# the internet.
resource "google_compute_firewall" "allow_ssh" {
  name    = "${var.instance_name}-allow-ssh"
  network = local.network
  project = var.project_id

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  source_ranges = var.allowed_management_sources
  target_tags   = var.instance_tags

  description = "Allow SSH access to Containarium jump server (operator-only, VPC default)"
}

# gRPC daemon API. Phase 2.3: same shift to allowed_management_sources.
# The gRPC port is for daemon-to-daemon mTLS and operator CLI usage;
# it is never user-facing.
resource "google_compute_firewall" "allow_grpc" {
  count   = var.enable_containarium_daemon ? 1 : 0
  name    = "${var.instance_name}-allow-grpc"
  network = local.network
  project = var.project_id

  allow {
    protocol = "tcp"
    ports    = ["50051"]
  }

  source_ranges = var.allowed_management_sources
  target_tags   = var.instance_tags

  description = "Allow gRPC API access to Containarium daemon (operator-only, VPC default)"
}

# IAP SSH firewall rule (needed in VPC environments for IAP tunneling)
resource "google_compute_firewall" "allow_iap_ssh" {
  count   = var.enable_iap_firewall ? 1 : 0
