variables {
  project_id      = "ssh-ban-test"
  anon_door_addr  = "127.0.0.1:2223"
  anon_daemon_url = "https://daemon.example.test"
}

run "defaults" {
  command = plan
  assert {
    condition     = alltrue([for unit in [local.sshpiper_service_unit, local.sshpiper_anon_service_unit] : strcontains(unit, "ssh-ban-plugin") && strcontains(unit, "--max-failures 100") && strcontains(unit, "--ban-duration 300s")])
    error_message = "Both listeners must invoke the inspectable plugin at the existing policy defaults."
  }
  assert {
    condition     = alltrue([for unit in [local.sshpiper_service_unit, local.sshpiper_anon_service_unit] : strcontains(unit, "ssh-upstream-plugin") && length(regexall("--upstream-control-socket /run/containarium/ssh-upstream/control.sock", unit)) >= 2])
    error_message = "Routing and source accounting in both listeners must share one guarded controller."
  }
  assert {
    condition = alltrue([for unit in [local.sshpiper_service_unit, local.sshpiper_anon_service_unit] : strcontains(unit, "After=network.target containarium-sentinel.service") && strcontains(unit, "BindsTo=containarium-sentinel.service") && strcontains(unit, "PartOf=containarium-sentinel.service")])
    error_message = "Both guarded listeners must follow the lifetime and startup ordering of the controller owner."
  }
}

run "custom_policy" {
  command = plan
  variables {
    ssh_ban_max_failures     = 240
