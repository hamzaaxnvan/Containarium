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
// install (no file provisioner, no sentinel binary server set up yet).
//
// #1784 (design doc, Rollout Phase 2): the host installs the containariumd-*
// server artifact; containarium-* is the client build from Phase 2 on. The
// startup script installs it at /usr/local/bin/containariumd (#1781).
locals {
  containarium_binary_url = var.containarium_binary_url != "" ? var.containarium_binary_url : (
    "https://github.com/footprintai/Containarium/releases/download/v${var.containarium_version}/containariumd-linux-amd64"
  )
}

module "containarium" {
  source = "../modules/containarium"

  // Identity
  project_id    = var.project_id
  region        = var.region
  zone          = var.zone
  instance_name = var.instance_name

  // Sizing — small but real
  machine_type   = var.machine_type
  boot_disk_size = 30

  // Spot + persistent disk: containers survive preemption. Demo
  // viewers see real recovery if they hold the cluster for a few days.
  use_spot_instance     = true
  use_persistent_disk   = true
  data_disk_size        = 100
  data_disk_type        = "pd-balanced"
  enable_disk_snapshots = false // optional; turn on if you keep the cluster around

  // Sentinel — required for the agent-native demo (Caddy + sshpiper
  // hostname routing live here).
  enable_sentinel         = true
