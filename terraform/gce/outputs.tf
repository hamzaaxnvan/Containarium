# =============================================================================
# Dev Consumer Outputs — forwarded from containarium module
# =============================================================================

output "jump_server_ip" {
  description = "Public IP address (sentinel IP when sentinel is enabled)"
  value       = module.containarium.jump_server_ip
}

output "jump_server_name" {
  description = "Name of the jump server instance"
  value       = module.containarium.jump_server_name
}

output "jump_server_zone" {
  description = "Zone where the jump server is deployed"
  value       = module.containarium.jump_server_zone
}

output "ssh_command" {
  description = "SSH command to connect to the jump server"
  value       = module.containarium.ssh_command
}

output "sentinel_enabled" {
  description = "Whether sentinel HA is enabled"
  value       = module.containarium.sentinel_enabled
}

