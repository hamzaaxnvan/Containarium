# =============================================================================
# Containarium Terraform Module — Variables
# =============================================================================
# Unified variable definitions for both dev and production consumers.

variable "ssh_upstream_failure_budget" {
  description = "Aggregate backend jail-risk budget, shared by main and anonymous SSH. Default leaves one unit below the tested maxretry=3 profile."
  type        = number
  default     = 2
  validation {
    condition     = var.ssh_upstream_failure_budget >= 1 && var.ssh_upstream_failure_budget <= 65536 && floor(var.ssh_upstream_failure_budget) == var.ssh_upstream_failure_budget
    error_message = "Upstream failure budget must be an integer from 1 to 65536."
  }
}

variable "ssh_upstream_risk_per_attempt" {
  description = "Conservative matching jail records per admitted SSH setup; verify against the actual backend filter."
  type        = number
  default     = 1
  validation {
    condition     = var.ssh_upstream_risk_per_attempt >= 1 && var.ssh_upstream_risk_per_attempt <= 65536 && floor(var.ssh_upstream_risk_per_attempt) == var.ssh_upstream_risk_per_attempt
    error_message = "Per-attempt risk must be an integer from 1 to 65536."
  }
}

variable "ssh_upstream_window_seconds" {
  description = "Verified backend findtime, in seconds. Exhausted risk waits for this window; initial installation also waits once."
  type        = number
  default     = 600
  validation {
    condition     = var.ssh_upstream_window_seconds >= 1 && var.ssh_upstream_window_seconds <= 86400 && floor(var.ssh_upstream_window_seconds) == var.ssh_upstream_window_seconds
    error_message = "Upstream risk window must be an integer from 1 to 86400 seconds."
  }
}

variable "ssh_ban_max_failures" {
  description = "Sentinel SSH failed-callback threshold per source IP."
  type        = number
  default     = 100
  validation {
    condition     = var.ssh_ban_max_failures >= 1 && var.ssh_ban_max_failures <= 4294967295 && floor(var.ssh_ban_max_failures) == var.ssh_ban_max_failures
    error_message = "SSH ban max failures must be a positive uint32 integer."
  }
}

variable "ssh_ban_duration_seconds" {
  description = "Fixed sentinel SSH failure window from the first connection."
  type        = number
  default     = 300
  validation {
    condition     = var.ssh_ban_duration_seconds >= 1 && var.ssh_ban_duration_seconds <= 86400 && floor(var.ssh_ban_duration_seconds) == var.ssh_ban_duration_seconds
    error_message = "SSH ban duration must be an integer from 1 to 86400 seconds."
  }
}

variable "ssh_ban_ignore_cidrs" {
  description = "Narrow IPv4/IPv6 CIDRs exempt from sentinel IP banning, never key authentication."
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for prefix in var.ssh_ban_ignore_cidrs : can(cidrhost(prefix, 0)) && can(regex("^[0-9a-fA-F:.]+/[0-9]+$", prefix)) && !can(regex("(?i)::ffff:", prefix))])
    error_message = "SSH ban exemptions must be IPv4/IPv6 CIDRs without whitespace, flags, or mapped IPv4 addresses."
  }
}

variable "ssh_ban_audit_records" {
  description = "Record failed SSH authentications and bans from both listeners' ssh-ban-plugin into the SSH session records file the shipper already delivers to the backend's audit chain (#2419). Needs a containariumd whose ssh-ban-plugin has --records-file; an older binary rejects the flag and sshpiper will not start, so this is off by default. See docs/SENTINEL-SSH-SESSION-AUDIT.md."
  type        = bool
  default     = false
}

variable "ssh_authz_enforce" {
  description = "Refuse SSH keys the backend's live authorization denies (fails closed after the 5m grace). False only audits. See docs/SENTINEL-SSH-AUTHZ.md."
  type        = bool
  default     = false
}

# -----------------------------------------------------------------------------
# Project & Region
# -----------------------------------------------------------------------------

variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "GCP zone"
  type        = string
  default     = "us-central1-a"
}

# -----------------------------------------------------------------------------
# Instance Configuration
# -----------------------------------------------------------------------------

variable "instance_name" {
  description = "Name of the GCE instance"
  type        = string
  default     = "containarium-jump"
}

variable "machine_type" {
  description = "GCE machine type"
  type        = string
  default     = "n2-standard-8"

  validation {
    condition     = can(regex("^(n1|n2|n2d|n4|e2|c3|c3d|c4|c4a)-(standard|highmem|highcpu)-[0-9]+$", var.machine_type))
    error_message = "Must be a valid GCE machine type (e.g., c3d-highmem-8, n2-standard-4)"
  }
}

variable "os_image" {
  description = "OS image for the instance"
  type        = string
  default     = "ubuntu-os-cloud/ubuntu-2404-lts-amd64"
}

variable "boot_disk_size" {
  description = "Boot disk size in GB"
  type        = number
  default     = 500

  validation {
    condition     = var.boot_disk_size >= 10 && var.boot_disk_size <= 2000
    error_message = "Boot disk size must be between 10 and 2000 GB"
  }
}

variable "boot_disk_type" {
  description = "Boot disk type"
  type        = string
  default     = "pd-balanced"

  validation {
    condition     = contains(["pd-standard", "pd-balanced", "pd-ssd", "hyperdisk-balanced", "hyperdisk-throughput"], var.boot_disk_type)
    error_message = "Boot disk type must be pd-standard, pd-balanced, pd-ssd, hyperdisk-balanced, or hyperdisk-throughput"
  }
}

variable "boot_disk_auto_delete" {
  description = "Whether the spot jump-server's boot disk is deleted when the instance is deleted. Set false to protect a disk that predates or is managed outside this module's lifecycle."
  type        = bool
  default     = true
}

# -----------------------------------------------------------------------------
# Encryption (CMEK)
# -----------------------------------------------------------------------------

variable "kms_key_self_link" {
  description = <<-EOT
    Optional customer-managed KMS key for encrypting the backend and sentinel
    disks (boot + attached data disk). Provide a full self_link, e.g.
    "projects/<project>/locations/<region>/keyRings/<ring>/cryptoKeys/<key>".

    Default empty string keeps Google-managed encryption (the GCE default).
    Set this for compliance-bound deployments that need customer-managed keys.

    The compute service account on the project must have
    roles/cloudkms.cryptoKeyEncrypterDecrypter on the named key before apply
    succeeds, otherwise disk creation fails with a permission error.
  EOT
  type        = string
  default     = ""
}

# -----------------------------------------------------------------------------
# Networking
# -----------------------------------------------------------------------------

variable "network_self_link" {
  description = "VPC network self_link. If empty, uses default network."
  type        = string
  default     = ""
}

variable "subnetwork_self_link" {
  description = "Subnetwork self_link. If empty, uses default."
  type        = string
  default     = ""
}

variable "instance_tags" {
  description = "Network tags for the jump server instance"
  type        = list(string)
  default     = ["containarium-jump-server"]
}

# -----------------------------------------------------------------------------
# SSH & Security
# -----------------------------------------------------------------------------

variable "admin_ssh_keys" {
