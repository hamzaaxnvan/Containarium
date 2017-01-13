package engine

import "strings"

// CredentialAlertStatusScript applies #2790's alert policy without changing the
// credential-status RPC: only >24h expiry or a failed authenticated run means
// expired for the watcher. The runtime marker contains no credential data.
func CredentialAlertStatusScript(n Name) (string, bool) {
	script, ok := CredentialStatusScript(n)
	if !ok || n != NameClaude {
		return script, ok
