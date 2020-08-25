# Single Server Example: Spot Instance with Persistent Disk
# Use case: 20-50 users, tight budget, can tolerate 2-5 min downtime

# GCP Project
project_id = "your-gcp-project-id"
region     = "us-central1"
zone       = "us-central1-a"

# Single Server Configuration
enable_horizontal_scaling = false # Single jump server
instance_name             = "containarium-jump"
machine_type              = "n2-standard-8" # 8 vCPU, 32GB RAM

# Cost Optimization - Maximum Savings!
use_spot_instance   = true # 76% cheaper than regular VM
use_persistent_disk = true # Containers survive spot termination

# Storage
boot_disk_size = 100 # GB
data_disk_size = 500 # GB - all container data here
data_disk_type = "pd-balanced"

# Backup
enable_disk_snapshots = true # Daily snapshots

# SSH Access
admin_ssh_keys = {
  admin = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... admin@example.com"
}

# Security
allowed_ssh_sources = [
