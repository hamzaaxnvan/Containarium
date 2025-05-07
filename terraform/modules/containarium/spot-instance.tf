# =============================================================================
# Containarium Terraform Module — Spot Instance + Persistent Disk
# =============================================================================

# -----------------------------------------------------------------------------
# Persistent disk for Incus data (survives spot termination)
# -----------------------------------------------------------------------------

resource "google_compute_disk" "incus_data" {
  count = var.use_persistent_disk ? 1 : 0

  name    = "${var.instance_name}-incus-data"
  type    = var.data_disk_type
  zone    = var.zone
  size    = var.data_disk_size
  project = var.project_id

  labels = merge(
    var.labels,
    {
      component = "containarium"
      role      = "storage"
      data_type = "incus-containers"
    }
  )

  dynamic "disk_encryption_key" {
    for_each = var.kms_key_self_link == "" ? [] : [1]
    content {
      kms_key_self_link = var.kms_key_self_link
    }
  }

  lifecycle {
    prevent_destroy = true
  }
}

# -----------------------------------------------------------------------------
# Snapshot schedule for daily backups
# -----------------------------------------------------------------------------

resource "google_compute_resource_policy" "incus_data_snapshot_policy" {
  count = var.enable_disk_snapshots ? 1 : 0

  name    = "${var.instance_name}-snapshot-policy"
  region  = var.region
  project = var.project_id

  snapshot_schedule_policy {
    schedule {
      daily_schedule {
        days_in_cycle = 1
        start_time    = "03:00"
      }
    }

    retention_policy {
      max_retention_days    = 30
      on_source_disk_delete = "KEEP_AUTO_SNAPSHOTS"
    }

    snapshot_properties {
      labels = {
        snapshot_type = "automated"
        resource      = "containarium"
      }
      storage_locations = [var.region]
      guest_flush       = true
    }
  }
}

# -----------------------------------------------------------------------------
# Spot VM Instance
# -----------------------------------------------------------------------------

locals {
  spot_vm_name = local.use_sentinel && var.spot_vm_name_suffix != "" ? "${var.instance_name}${var.spot_vm_name_suffix}" : var.instance_name

  # The incus bridge gateway. startup-spot.sh creates incusbr0 at a fixed
  # ipv4.address=10.0.3.1/24, so this is a module constant, not a guess.
  incus_bridge_gateway = "10.0.3.1/32"

  # Caddy runs INSIDE the containarium-core-caddy LXC, so PROXY v2 frames from
  # the sentinel cross the bridge and arrive with a source of 10.0.3.1 — never
  # the sentinel's own address. A trust list naming only the sentinel (or its
  # whole subnet) therefore matches nothing: Caddy treats every frame as
  # untrusted, discards it, and logs the bridge as the client.
  #
  # That failure is silent — no error, well-formed logs, just the wrong IP in
  # them — and it defeats anything keyed on client IP. Both of our production
  # primaries shipped this way; one logged 1037 of 1038 requests as 10.0.3.1
  # before it was noticed (#1668).
  #
  # So append the gateway rather than relying on operators to know about the
  # LXC hop. /32 not /24: trusting the whole bridge would let any container on
  # it assert an arbitrary client_ip via its own PROXY header, trading a
  # logging bug for a spoofing hole. distinct() keeps it idempotent when an
  # operator has already listed it.
  proxy_protocol_trusted_effective = var.enable_proxy_protocol ? distinct(concat(var.proxy_protocol_trusted_cidrs, [local.incus_bridge_gateway])) : var.proxy_protocol_trusted_cidrs
}

resource "google_compute_instance" "jump_server_spot" {
