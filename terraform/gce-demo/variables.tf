// Identity ------------------------------------------------------------

variable "project_id" {
  description = "GCP project ID. The cluster, DNS records, and IAM resources all live here."
  type        = string
}

variable "region" {
  description = "GCP region for the spot VM and sentinel."
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "GCP zone (single-AZ for the demo)."
  type        = string
  default     = "us-central1-a"
}

variable "instance_name" {
  description = "Name prefix for the demo instances. The spot VM is <name>, the sentinel is <name>-sentinel."
  type        = string
  default     = "containarium-demo"
}

variable "machine_type" {
  description = "GCE machine type for the spot backend VM. e2-standard-2 (2 vCPU, 8 GB) is enough for the demo container plus Incus overhead. Note: the upstream module's regex rejects shared-core types (e2-medium / e2-small / e2-micro)."
  type        = string
  default     = "e2-standard-2"
}

// Versioning ----------------------------------------------------------

variable "containarium_version" {
  description = "Containarium version to install on the demo VM. Should match a tagged release at https://github.com/footprintai/Containarium/releases. Note: --proxy-protocol on the sentinel requires v0.16.7+ — older versions silently no-op'd the flag in simple-proxy mode."
  type        = string
  default     = "0.16.7"
}

variable "base_domain" {
  description = "Public hostname the demo daemon serves at. Containers exposed via `expose_port` get subdomains of this (e.g. blog.<base_domain>). The cluster must have wildcard DNS for *.<base_domain> pointing at the sentinel's IP — Cloudflare-managed for the production demo deployment. Empty disables app-hosting."
  type        = string
  default     = "demo.containarium.dev"
}

variable "containarium_binary_url" {
  description = "Override the binary download URL. Empty means derive from containarium_version (the standard release URL pattern)."
  type        = string
  default     = ""
}

// Access --------------------------------------------------------------

variable "admin_ssh_keys" {
  description = "Map of admin username → SSH public key string. The admin can SSH into the sentinel for token issuance."
  type        = map(string)
}

variable "allowed_ssh_sources" {
  description = "CIDR blocks allowed to SSH to the sentinel on port 22. Restrict to your egress IP for a real demo; broad for a quick test."
  type        = list(string)
