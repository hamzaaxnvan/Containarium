//go:build !containarium_client

package cmd

// resolveServerAddr is the identity function under containariumd: the
// credentials-file default_server fallback is client-only (see
