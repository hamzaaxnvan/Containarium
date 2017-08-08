package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Purpose-separated secret derivation.
//
// Some daemon-to-component channels need a *shared* secret rather than a
// bearer JWT: the component has to verify the caller, and handing it the JWT
// signing key would hand it the power to mint tokens (HS256 — the verification
// key IS the signing key). The in-box A2A server is the first such case
// (#2125): it must be able to tell "the daemon sent this task" from "a peer box
// sent this task", inside a container that is itself the untrusted party.
//
// DeriveSharedSecret answers that with an HKDF-shaped derivation from the
// daemon's existing JWT signing key, so:
//
//   - there is nothing new for an operator to configure, and nothing new to
//     persist — the daemon recomputes any component's secret on demand, which
//     survives a daemon restart;
//   - the derivation is one-way, so a component that holds its own derived
//     secret learns nothing about the signing key or about any other
//     component's secret;
