// Package tracker will hold the daemon-side tracker broker — agent-visible
// GitHub/GitLab issue and change-request verbs backed by a daemon-custody
// credential (#1920). This file lands ahead of the rest of the package
// (#1922, #1923) because the broker-only secret mode (#1921) needs a
// named seam to plug into. See docs/architecture/agent-tracker-broker.md.
package tracker
