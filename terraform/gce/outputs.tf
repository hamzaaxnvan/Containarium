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

