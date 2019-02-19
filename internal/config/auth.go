package config

// EnvStrictScopes is the CONTAINARIUM_AUTH_* namespace's single variable
// (#1679): armed, a token with a missing/nil `scopes` claim is rejected
// instead of treated as unrestricted. Off by default — scopes fail open
// until an operator has measured the unscoped-token population (see
// TokensService.GetUnscopedTokenReport) and opts in.
const EnvStrictScopes = "CONTAINARIUM_STRICT_SCOPES"

// Auth is the typed view of the auth-related CONTAINARIUM_* namespace.
