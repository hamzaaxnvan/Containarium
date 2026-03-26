package config

// BYOC (bring-your-own-compute) host-daemon variable names.
//
// CONTAINARIUM_BYOC_INGRESS_ADDR opts a host into the sentinel-terminate BYOC
// public-HTTP-ingress data path (#733 slice 3). When set to a loopback-scoped
// address ("127.0.0.1:<port>"), the daemon's edge Caddy adds one extra plaintext
