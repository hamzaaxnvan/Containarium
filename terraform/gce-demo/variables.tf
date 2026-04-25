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
