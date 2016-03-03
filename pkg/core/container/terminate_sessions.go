package container

import "os/exec"

// TerminateAccountSessions ends every live session and process account holds
// inside containerName, and changes nothing else: the account, its home,
// its sudoers entry and its authorized_keys all stay. Shared by collaborator
// removal (which then deletes the account) and TerminateUserSessions
// (#2522, which deletes nothing).
//
// Both steps are best-effort: loginctl exists only on systemd-based images,
// and pkill exits 1 when nothing matched, so neither status is an error —
// the KILL is what drops the shell. It reports whether pkill matched (exit
// 0), i.e. whether the account had anything running to end. An account name
// that is not a valid Linux login (pkill would read "-1" as a flag) is
// refused without touching the box.
func (m *Manager) TerminateAccountSessions(containerName, account string) bool {
	if !isValidUsername(account) {
