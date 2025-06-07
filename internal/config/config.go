// Package config centralizes Containarium's environment-driven configuration.
//
// Containarium reads ~110 CONTAINARIUM_* environment variables. Historically
// each was read inline via os.Getenv at its point of use — frequently the same
// variable in many places — stringly-typed, with ad-hoc per-site defaults and
// no validation. That sprawl (hundreds of os.Getenv call sites) is the problem
// this package replaces, one namespace at a time, with:
//
//   - a typed struct per CONTAINARIUM_<NAMESPACE>_ prefix (the prefixes already
//     are the de-facto namespaces: SENTINEL_, K8S_, AWS_, VAULT_, …),
//   - exported Env* name constants so each variable name has a single home
//     instead of being a magic string repeated across the tree,
