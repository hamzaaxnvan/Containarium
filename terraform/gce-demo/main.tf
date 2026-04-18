// Demo deployment for the agent-native sandbox walkthrough.
//
// Provisions a single-host Containarium cluster with:
//   - Sentinel (e2-micro, free-tier) running sshpiper + Caddy
//   - Spot VM (e2-medium) running the daemon + Incus
//   - Persistent disk for container data
//   - Optional wildcard DNS record (*.<demo_subdomain>.<zone>) → sentinel
//
// Designed to be the smallest realistic deployment that supports the
// full agent-native demo flow (create → ssh → install → expose).
// Costs ~$30/month while running; teardown via terraform destroy.
//
// State is local (terraform.tfstate in this directory). For a long-
// lived demo cluster you may want to wire a GCS backend like the
// production deployment does (see terraform/gce/backend-prod.tf.example
// for the pattern).

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
  zone    = var.zone
}

// Derive the GitHub release URL when an explicit override isn't given.
// The startup script otherwise has no source for the binary on a fresh
