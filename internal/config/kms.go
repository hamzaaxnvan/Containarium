package config

// KMS / cloud-credential variable names. This namespace differs from the others
// in this package: its values are consumed inside pkg/core/secrets, which pulls
// in cloud provider SDKs. To keep internal/config lightweight (stdlib-only, so
// every importer stays cheap), config owns only the *names* here — the single
// source of truth for the namespace's identity. The read+resolve+validate
// factory that turns these into a KMS client lives in the app layer
// (internal/secrets.LoadKMSClient), where the SDK dependency already exists.
//
// Several names contain "secret"/"token"; gosec G101 flags constant identifiers
// like those assigned a string literal as "potential hardcoded credentials".
// These are env-var NAMES, not credentials, so each is annotated //nosec G101.
const (
	// KMS backend selector: none | inproc | vault | gcp | aws.
	EnvKMSBackend = "CONTAINARIUM_KMS_BACKEND"

