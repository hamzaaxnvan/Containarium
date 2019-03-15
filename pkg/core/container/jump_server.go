package container

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Note: As of Go 1.20, rand is automatically seeded

// useraddMu serializes useradd invocations within the same daemon
// process. Cloud #163 — two concurrent box creates (CI provisioning
// in parallel) race the /etc/passwd lock; even though we use `flock
// -w 30` for the cross-process case, two in-process goroutines can
// both win flock + both call useradd before the first finishes, at
// which point useradd's own lock contention surfaces as the
// "cannot lock /etc/passwd" retry storm in the logs.
//
// Holding this mutex around the entire flock+useradd path means
// only one useradd at a time per daemon process, regardless of how
// many CI jobs are provisioning boxes concurrently. The cross-process
// case still relies on the existing flock + stale-lock cleanup.
var useraddMu sync.Mutex

// CreateJumpServerAccount creates a proxy-only user account on the jump server
// The account is configured with /usr/sbin/nologin shell, preventing direct shell access
// while still allowing SSH ProxyJump to work for accessing containers.
func CreateJumpServerAccount(username string, sshPublicKey string, verbose bool) error {
	if verbose {
		fmt.Printf("  Creating jump server account: %s (proxy-only)\n", username)
	}

	// Validate inputs
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if sshPublicKey == "" {
		return fmt.Errorf("SSH public key cannot be empty")
	}

	// Sanitize username (allow only alphanumeric, dash, underscore)
	if !isValidUsername(username) {
		return fmt.Errorf("invalid username: must contain only letters, numbers, dash, and underscore")
	}

	// Check if user already exists
	if userExists(username) {
		if verbose {
			fmt.Printf("  Jump server account %s already exists, updating SSH key and shell\n", username)
		}
		// Ensure shell is set to nologin (in case it was created with wrong shell)
		if err := ensureProxyOnlyShell(username, verbose); err != nil {
			return fmt.Errorf("failed to ensure proxy-only shell: %w", err)
		}
		// Self-heal a previously-locked account (created before this unlock fix).
		if err := ensureAccountUnlocked(username); err != nil {
			return err
		}
		return updateUserSSHKey(username, sshPublicKey, verbose)
	}

	// Create user with nologin shell (proxy-only, no shell access)
	// Wait for lock files to clear and retry useradd if needed
	if err := retryUseraddWithLockWait(username, verbose); err != nil {
		return err
	}

	// Unlock: useradd creates locked ("!") accounts, and sshd with UsePAM=no
	// (the hardened jump/BYOC-host setting) refuses a locked account even for
	// public-key auth — the box accepts the client key but the sentinel->host
	// upstream hop dies with "account is locked" (#687/#808). The Manager
	// creates boxes through THIS function (manager.go), so the unlock must live
	// here too; EnsureJumpServerAccount only covered the other create path.
	if err := ensureAccountUnlocked(username); err != nil {
		_ = exec.Command("userdel", "-r", username).Run()
		return err
	}

	// Setup SSH key
	if err := setupUserSSHKey(username, sshPublicKey, verbose); err != nil {
		// Cleanup on failure
		_ = exec.Command("userdel", "-r", username).Run()
		return err
	}

	// Sudoers entry for incus access. Required when the user's login shell is
	// containarium-shell (proxies into the container via `sudo incus exec`).
	// Harmless when the shell is nologin (no login = no sudo).
	sudoersEntry := fmt.Sprintf("%s ALL=(root) NOPASSWD: /usr/bin/incus\n", username)
	sudoersPath := fmt.Sprintf("/etc/sudoers.d/containarium-%s", username)
	if err := os.WriteFile(sudoersPath, []byte(sudoersEntry), 0440); err != nil { // #nosec G306 -- sudoers requires 0440
		_ = exec.Command("userdel", "-r", username).Run()
		return fmt.Errorf("failed to write sudoers: %w", err)
	}

	if verbose {
		fmt.Printf("  ✓ Jump server account created: %s\n", username)
	}

	return nil
}

// DeleteJumpServerAccount removes a user account from the jump server
func DeleteJumpServerAccount(username string, verbose bool) error {
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}

	if !userExists(username) {
		if verbose {
			fmt.Printf("  Jump server account %s does not exist, skipping\n", username)
		}
		return nil // Already deleted or never existed
	}

	if verbose {
		fmt.Printf("  Deleting jump server account: %s\n", username)
	}

	// Delete user and home directory - wait for locks to clear
	if err := waitForLocksAndRun(func() error {
		return runUserdel(username, verbose)
	}, verbose); err != nil {
		return err
	}

	if verbose {
		fmt.Printf("  ✓ Jump server account deleted: %s\n", username)
	}

	return nil
}

// runUserdel deletes the account, tolerating the one failure mode that is
// routine rather than exceptional: `userdel: user X is currently used by
// process N` (exit 8), which happens whenever the caller still has an SSH
// session open to the box they just deleted (#1035). The box itself is
// already gone by the time delete-cascade runs, so any surviving session is
// a dead shell with nothing behind it — killing it and retrying once is
// strictly better than leaving an orphaned host account for the reaper.
//
// Every other userdel failure propagates unchanged.
func runUserdel(username string, verbose bool) error {
	// #nosec G204 -- username is gated by userExists + isValidUsername below
	out, err := exec.Command("userdel", "-r", username).CombinedOutput()
	if err == nil {
		return nil
	}
	if !userdelBusy(out) || !isValidUsername(username) {
		return fmt.Errorf("failed to delete user %s: %w\nOutput: %s", username, err, out)
	}

	if verbose {
		fmt.Printf("  %s still has a live session; terminating it and retrying userdel\n", username)
	}
	// pkill exits 1 when nothing matched — a benign race (the session ended
	// between the two calls), so the status is deliberately ignored.
	// #nosec G204 -- username validated by isValidUsername above
	_ = exec.Command("pkill", "-KILL", "-u", username).Run()
	time.Sleep(500 * time.Millisecond)

	// #nosec G204 -- username validated by isValidUsername above
	if out, err := exec.Command("userdel", "-r", username).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to delete user %s after terminating its sessions: %w\nOutput: %s", username, err, out)
	}
	return nil
}

// userdelBusy reports whether userdel's output is the "user is currently used
// by process" refusal. Matched on the message rather than the exit status
// because shadow-utils reuses exit 8 for a couple of unrelated conditions.
func userdelBusy(output []byte) bool {
	return strings.Contains(string(output), "currently used by process")
}

// EnsureJumpServerAccount creates a host-level user with containarium-shell
// as the login shell, enabling SSH access through sshpiper into the user's
// Incus container. This is called automatically when a container is created.
// It is idempotent — if the account already exists, it just ensures the shell
// and permissions are correct.
func EnsureJumpServerAccount(username string) error {
	if !isValidUsername(username) {
		return fmt.Errorf("invalid username: %s", username)
	}

	shellPath := "/usr/local/bin/containarium-shell"

	if userExists(username) {
		// Ensure shell is containarium-shell
		// #nosec G204 -- username validated by isValidUsername above (alphanumeric, dash, underscore only)
		_ = exec.Command("usermod", "-s", shellPath, username).Run()
	} else {
		// Create user with containarium-shell
		// -K SUB_UID_COUNT=0 / SUB_GID_COUNT=0: this account only ever runs
		// containarium-shell (exec into the tenant's own container) — it
		// never needs a user-namespace subuid/subgid mapping. Without this,
		// useradd auto-allocates one from /etc/subuid's small free window
		// below Incus's own root reservation (#1531): found live that the
		// window exhausts after ~10-14 accounts on a fresh host, after
		// which every subsequent EnsureJumpServerAccount call fails outright
		// (useradd exit 16) — not a degraded state, no jump-server account
		// at all. retryUseraddWithLockWait (CreateJumpServerAccount's own
		// useradd, used by the SSH-key create path) already carries these
		// same two flags; this call site was the one gap.
		// #nosec G204 -- username validated by isValidUsername above
		if err := exec.Command("useradd", "-m", "-s", shellPath,
			"-K", "SUB_UID_COUNT=0",
			"-K", "SUB_GID_COUNT=0",
			"-c", fmt.Sprintf("Containarium user - %s", username),
			username).Run(); err != nil {
			return fmt.Errorf("useradd failed: %w", err)
		}
	}

	// Everything below is idempotent and runs on BOTH the freshly-created and
	// the already-exists paths. The already-exists path previously only fixed
	// the shell and returned — so an account left in a bad state (most
	// importantly: locked) never self-healed. Ensuring full state every call
	// makes a misprovisioned account recover on the next create/reconcile.
	return ensureJumpAccountState(username)
}

// ensureJumpAccountState applies the idempotent host-side state a jump account
// needs to be reachable through sshpiper: unlocked, world-readable home,
// owned .ssh dir, and the incus sudoers entry. Safe to re-run.
func ensureJumpAccountState(username string) error {
	if err := ensureAccountUnlocked(username); err != nil {
		return err
	}

	// Set home dir permissions (sshd requires 755 or stricter).
	_ = os.Chmod(fmt.Sprintf("/home/%s", username), 0755) // #nosec G302 -- sshd requires home dir to be world-readable

	// Create .ssh dir
	sshDir := fmt.Sprintf("/home/%s/.ssh", username)
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("failed to create .ssh dir: %w", err)
	}
	// #nosec G204 -- username validated by isValidUsername (callers)
	_ = exec.Command("chown", "-R", username+":"+username, sshDir).Run()

	// Sudoers entry for incus access (containarium-shell needs it)
	sudoersEntry := fmt.Sprintf("%s ALL=(root) NOPASSWD: /usr/bin/incus\n", username)
	sudoersPath := fmt.Sprintf("/etc/sudoers.d/containarium-%s", username)
	if err := os.WriteFile(sudoersPath, []byte(sudoersEntry), 0440); err != nil { // #nosec G306 -- sudoers requires 0440
		return fmt.Errorf("failed to write sudoers: %w", err)
	}

	return nil
}

// ensureAccountUnlocked guarantees the account's password is disabled but the
// account is NOT locked from sshd's perspective (shadow password must not start
// with "!").
//
// useradd creates accounts locked ("!" password). With `UsePAM no` — the
// hardened setting on jump/BYOC hosts — OpenSSH performs its OWN locked-account
// check and refuses a locked account *even for public-key auth*. The box still
// accepts the client key downstream, but the sshpiper→host upstream hop dies
// with "User <u> not allowed because account is locked", which surfaces on the
// client as a confusing "authenticated with partial success" loop ending in
// Permission denied. So the unlock is load-bearing, must run on every ensure
// (a single earlier failure must not strand the box), and its result must be
// verified rather than swallowed.
//
// `usermod -p '*'` sets the password field to "*" — "no valid password" but NOT
// the "!" lock prefix; it is idempotent, safe to re-run, and (unlike
// `passwd -d`'s empty password) portable across distros. OpenSSH accepts "*"
// (and an empty field) for public-key auth; it rejects only "!"-prefixed.
func ensureAccountUnlocked(username string) error {
	// #nosec G204 -- callers validate username via isValidUsername
	if out, err := exec.Command("usermod", "-p", "*", username).CombinedOutput(); err != nil {
		return fmt.Errorf("unlock account %s: %w (output: %s)", username, err, strings.TrimSpace(string(out)))
	}

	// Verify the account is no longer locked. We must read the shadow password
	// field directly, NOT `passwd -S`: that tool reports both "!" (truly locked,
	// sshd rejects) and "*" (the value we just set, which sshd accepts) as "L",
	// so it cannot tell a working account from a broken one. sshd's locked check
	// keys off the "!" prefix, so we do the same.
	// #nosec G204 -- callers validate username via isValidUsername
	out, err := exec.Command("getent", "shadow", username).Output()
	if err != nil {
		// getent/shadow unavailable or not permitted: the usermod above already
		// succeeded, so don't fail the whole ensure on a verification gap.
		return nil
	}
	if shadowAccountLocked(string(out)) {
		// Deliberately does NOT echo the shadow line (it carries the hash).
		return fmt.Errorf("account %s still locked after unlock attempt", username)
	}
	return nil
}

// shadowAccountLocked reports whether a getent-shadow line shows an account
// OpenSSH treats as locked: the password field (2nd colon-separated field)
// starts with "!". An empty field ("NP") or "*" ("disabled, not locked") is NOT
// locked — both permit public-key auth under `UsePAM no`. A line we can't parse
// is treated as not-locked so a quirky environment can't wedge provisioning.
func shadowAccountLocked(getentShadowLine string) bool {
	line := strings.TrimRight(getentShadowLine, "\r\n")
	parts := strings.SplitN(line, ":", 3)
	if len(parts) < 2 {
		return false
	}
	return strings.HasPrefix(parts[1], "!")
}

// isValidUsername checks if username contains only allowed characters
func isValidUsername(username string) bool {
	if len(username) == 0 || len(username) > 32 {
		return false
	}

	for _, ch := range username {
		if (ch < 'a' || ch > 'z') &&
			(ch < 'A' || ch > 'Z') &&
			(ch < '0' || ch > '9') &&
			ch != '-' && ch != '_' {
			return false
		}
	}

	// Cannot start with digit or dash
	firstChar := rune(username[0])
	if (firstChar >= '0' && firstChar <= '9') || firstChar == '-' {
		return false
	}

	return true
}

// userExists checks if a system user exists
func userExists(username string) bool {
	cmd := exec.Command("id", username)
	return cmd.Run() == nil
}

// UserExists is the exported version of userExists for use by CLI commands
func UserExists(username string) bool {
	return userExists(username)
}

// ExtractSSHKeys extracts EVERY SSH public key authorized inside a container,
// read from /home/{username}/.ssh/authorized_keys (falling back to root's).
//
// Returns all of them, in file order, because authorized_keys is a list: a box
// can legitimately be reachable by an operator key, a runner key and one key
// per collaborator machine. Returning only the first made recovery silently
// revoke everyone else, and which key survived was decided by file order
// (#1477).
func (m *Manager) ExtractSSHKeys(containerName, username string, verbose bool) ([]string, error) {
	// Check if container is running
	info, err := m.incus.GetContainer(containerName)
	if err != nil {
		return nil, fmt.Errorf("container not found: %w", err)
	}

	// If container is stopped, try to start it temporarily
	wasStarted := false
	if info.State != "Running" {
		if verbose {
			fmt.Printf("       Starting container %s to extract SSH key...\n", containerName)
		}
		if err := m.incus.StartContainer(containerName); err != nil {
			return nil, fmt.Errorf("failed to start container: %w", err)
		}
		wasStarted = true
		// Wait for container to be ready
		time.Sleep(3 * time.Second)
	}

	// Try to read the SSH key from the container
	authorizedKeysPath := fmt.Sprintf("/home/%s/.ssh/authorized_keys", username)

	if verbose {
		fmt.Printf("       Reading SSH key from %s:%s\n", containerName, authorizedKeysPath)
	}

	keyContent, err := m.incus.ReadFile(containerName, authorizedKeysPath)
	if err != nil {
		// Try root's authorized_keys as fallback
		if verbose {
			fmt.Printf("       Primary path failed, trying /root/.ssh/authorized_keys...\n")
		}
		keyContent, err = m.incus.ReadFile(containerName, "/root/.ssh/authorized_keys")
		if err != nil {
			// Stop container if we started it
			if wasStarted {
				_ = m.incus.StopContainer(containerName, false)
			}
			return nil, fmt.Errorf("could not read SSH key from container: %w", err)
		}
	}

	// Stop container if we started it
	if wasStarted {
		if verbose {
			fmt.Printf("       Stopping container %s...\n", containerName)
		}
		_ = m.incus.StopContainer(containerName, false)
	}

	keys := parseAuthorizedKeys(string(keyContent))
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid SSH key found in authorized_keys")
	}
	return keys, nil
}

// ExtractSSHKey returns just the first authorized key, for callers that only
// need one. Prefer ExtractSSHKeys: anything that (re)provisions host access
// must carry the whole set, or it silently drops every key but one (#1477).
func (m *Manager) ExtractSSHKey(containerName, username string, verbose bool) (string, error) {
	keys, err := m.ExtractSSHKeys(containerName, username, verbose)
	if err != nil {
		return "", err
	}
	return keys[0], nil
}

// sshKeyPrefixes are the key types an authorized_keys line may start with.
var sshKeyPrefixes = []string{
	"ssh-rsa ",
	"ssh-ed25519 ",
	"ssh-ecdsa ",
	"ecdsa-sha2-",
	"ssh-dss ",
}

// parseAuthorizedKeys pulls every valid key line out of an authorized_keys
// file, preserving order and dropping exact duplicates.
//
// Pure and separate from the incus round-trip so the parsing rules are
// testable without a running container — the behaviour #1477 got wrong lived
// entirely in here.
//
// Comparison for de-duplication is on the WHOLE line, so the same key material
// carrying two different comments is kept twice. That is deliberate: this
// function cannot tell an intentional duplicate-with-different-comment from an
// accident, and keeping a key too many costs nothing, while dropping one
// removes somebody's access.
func parseAuthorizedKeys(content string) []string {
	var keys []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		valid := false
		for _, p := range sshKeyPrefixes {
			if strings.HasPrefix(line, p) {
				valid = true
				break
			}
		}
		if !valid || seen[line] {
			continue
		}
		seen[line] = true
		keys = append(keys, line)
	}
	return keys
}

// setupUserSSHKey sets up SSH key for a user
func setupUserSSHKey(username, sshPublicKey string, verbose bool) error {
	// Same lock as Add/Remove/Replace: this overwrites the file.
	defer LockAuthorizedKeys(username)()

	homeDir := filepath.Join("/home", username)
	sshDir := filepath.Join(homeDir, ".ssh")
	authKeysFile := filepath.Join(sshDir, "authorized_keys")

	// Create .ssh directory
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("failed to create .ssh directory: %w", err)
	}

	// Clean up SSH key (remove extra whitespace, ensure newline)
	cleanKey := strings.TrimSpace(sshPublicKey)

	// Write SSH key
	if err := os.WriteFile(authKeysFile, []byte(cleanKey+"\n"), 0600); err != nil {
		return fmt.Errorf("failed to write authorized_keys: %w", err)
	}

	// Set ownership (must run as root/sudo)
	cmd := exec.Command("chown", "-R", fmt.Sprintf("%s:%s", username, username), sshDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set ownership: %w\nOutput: %s", err, output)
	}

	if verbose {
		fmt.Printf("  ✓ SSH key configured for %s\n", username)
	}

	return nil
}

// updateUserSSHKey updates SSH key for existing user
func updateUserSSHKey(username, sshPublicKey string, verbose bool) error {
	// Same lock as Add/Remove/Replace: this is a read-then-append.
	defer LockAuthorizedKeys(username)()

	homeDir := filepath.Join("/home", username)
	sshDir := filepath.Join(homeDir, ".ssh")
	authKeysFile := filepath.Join(sshDir, "authorized_keys")

	// Ensure .ssh directory exists
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("failed to create .ssh directory: %w", err)
	}

	// Set ownership on .ssh directory
	cmd := exec.Command("chown", fmt.Sprintf("%s:%s", username, username), sshDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set .ssh ownership: %w\nOutput: %s", err, output)
	}

	// Read existing keys
	existingKeys := ""
	if data, err := os.ReadFile(authKeysFile); err == nil {
		existingKeys = string(data)
	}

	// Clean up new key
	cleanKey := strings.TrimSpace(sshPublicKey)

	// Check if key already exists
	if strings.Contains(existingKeys, cleanKey) {
		if verbose {
			fmt.Printf("  SSH key already exists for %s\n", username)
		}
		return nil // Key already present
	}

	// Append new key
	f, err := os.OpenFile(authKeysFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("failed to open authorized_keys: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(cleanKey + "\n"); err != nil {
		return fmt.Errorf("failed to write SSH key: %w", err)
	}

	// Set ownership on authorized_keys
	cmd = exec.Command("chown", fmt.Sprintf("%s:%s", username, username), authKeysFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set authorized_keys ownership: %w\nOutput: %s", err, output)
	}

	if verbose {
		fmt.Printf("  ✓ SSH key added for %s\n", username)
	}

	return nil
}

// containerShellPath is the path to the containarium-shell wrapper that
// proxies SSH sessions into Incus containers. When present, it's used
// instead of nologin so that SSH sessions land inside the container.
const containerShellPath = "/usr/local/bin/containarium-shell"

// getUserShell returns the shell to use for containarium host accounts.
// If containarium-shell wrapper exists (standalone/tunnel backends), use it
// so SSH sessions are proxied into the container. Otherwise use nologin
// (sentinel/GCP backends where sshpiper handles routing).
func getUserShell() string {
	if _, err := os.Stat(containerShellPath); err == nil {
		return containerShellPath
	}
	return "/usr/sbin/nologin"
}

// ensureProxyOnlyShell ensures the user's shell is set correctly
func ensureProxyOnlyShell(username string, verbose bool) error {
	shell := getUserShell()
	// Wait for locks to clear before modifying user
	if err := waitForLocksAndRun(func() error {
		cmd := exec.Command("usermod", "-s", shell, username)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to set shell for %s: %w\nOutput: %s", username, err, output)
		}
		return nil
	}, verbose); err != nil {
		return err
	}

	if verbose {
		fmt.Printf("  ✓ Shell set to %s for %s\n", shell, username)
	}

	return nil
}

// waitForLocksAndRun stops google-guest-agent, executes the function, then restarts it.
// On non-GCP hosts the stop/start sequence is meaningless (google-guest-agent
// doesn't exist) and produces misleading errors, so we just run fn directly.
// Issue #351.
func waitForLocksAndRun(fn func() error, verbose bool) error {
	if !isGCPHost() {
		if verbose {
			fmt.Printf("       Skipping google-guest-agent dance: host is not a GCP VM\n")
		}
		return fn()
	}

	if verbose {
		fmt.Printf("       Temporarily stopping google-guest-agent...\n")
	}

	// Stop google-guest-agent
	cmd := exec.Command("systemctl", "stop", "google-guest-agent")
	if output, err := cmd.CombinedOutput(); err != nil && verbose {
		fmt.Printf("       Warning: Failed to stop google-guest-agent: %v\n%s\n", err, output)
	}

	// Ensure we restart it when done
	defer func() {
		if verbose {
			fmt.Printf("       Restarting google-guest-agent...\n")
		}
		cmd := exec.Command("systemctl", "start", "google-guest-agent")
		if output, err := cmd.CombinedOutput(); err != nil && verbose {
			fmt.Printf("       Warning: Failed to restart google-guest-agent: %v\n%s\n", err, output)
		}
	}()

	// Wait a moment for the agent to fully stop
	time.Sleep(2 * time.Second)

	// Execute the operation
	return fn()
}

// retryUseraddWithLockWait stops google-guest-agent, creates user, then restarts it
func retryUseraddWithLockWait(username string, verbose bool) error {
	// Cloud #163 — serialize within-process so two concurrent box
	// creates don't race the /etc/passwd lock. The flock command
	// below handles the cross-process case; this handles
	// goroutine-vs-goroutine.
	useraddMu.Lock()
	defer useraddMu.Unlock()

	const maxRetries = 10 // Bumped from 5 (cloud #163). Combined with the new exponential backoff below, total ceiling is ~6min vs the old 10s — gives stale passwd locks enough room to clear or be force-removed.

	// Pre-useradd dance: stop google-guest-agent, wait for the
	// passwd lock to clear, force-remove if stuck. This is GCP-VM
	// specific — google-guest-agent races with local useradd over
	// /etc/.pwd.lock via OS Login. On a non-GCP backend (VirtualBox
	// lab spot, on-prem, AWS/Azure, …) the service doesn't exist
	// and the lockfile is held by something else legitimately;
	// running this dance just produces misleading "Access denied"
	// stderr and force-removes a lock we shouldn't touch. Issue
	// #351 — gate it on host-class detection.
	lockFiles := []string{"/etc/passwd.lock", "/etc/shadow.lock", "/etc/.pwd.lock", "/etc/group.lock"}
	if isGCPHost() {
		fmt.Printf("       Temporarily stopping google-guest-agent to avoid race condition...\n")

		// Stop google-guest-agent
		cmd := exec.Command("systemctl", "stop", "google-guest-agent")
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("       Warning: Failed to stop google-guest-agent: %v\n%s\n", err, output)
			fmt.Printf("       Proceeding anyway...\n")
		} else {
			fmt.Printf("       ✓ google-guest-agent stopped\n")
		}

		// Kill any remaining google processes that might be holding locks
		killGoogleProcesses(verbose)

		// Ensure we restart it when done
		defer func() {
			fmt.Printf("       Restarting google-guest-agent...\n")
			cmd := exec.Command("systemctl", "start", "google-guest-agent")
			if output, err := cmd.CombinedOutput(); err != nil {
				fmt.Printf("       Warning: Failed to restart google-guest-agent: %v\n%s\n", err, output)
			} else {
				fmt.Printf("       ✓ google-guest-agent restarted\n")
			}
		}()

		// Wait for the agent to fully stop and release locks
		fmt.Printf("       Waiting for agent to stop and release locks...\n")
		time.Sleep(2 * time.Second)

		// Check if agent actually stopped
		checkCmd := exec.Command("systemctl", "is-active", "google-guest-agent")
		if statusOutput, _ := checkCmd.CombinedOutput(); len(statusOutput) > 0 {
			fmt.Printf("       Agent status after stop: %s\n", strings.TrimSpace(string(statusOutput)))
		}

		// Wait for lock files to clear (up to 10 seconds)
		locksClear := false
		for attempt := 0; attempt < 10; attempt++ {
			allClear := true
			for _, lockFile := range lockFiles {
				if _, err := os.Stat(lockFile); err == nil {
					allClear = false
					if attempt == 0 {
						fmt.Printf("       Waiting for lock file to clear: %s\n", lockFile)
					}
					break
				}
			}
			if allClear {
				locksClear = true
				break
			}
			time.Sleep(1 * time.Second)
		}

		if !locksClear {
			fmt.Printf("       Warning: Lock files still present after waiting, forcing removal...\n")
			// Since we stopped google-guest-agent, any remaining lock files are stale
			// Forcibly remove them regardless of age
			forceRemoveLockFiles(lockFiles, verbose)
		} else {
			fmt.Printf("       ✓ All lock files cleared\n")
