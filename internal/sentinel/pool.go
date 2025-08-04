package sentinel

// Pool is a tag identifying a Containarium cluster ("pool"). Used as a
// routing key by the sentinel's primary registry, peer discovery, and the
// token policy. An empty Pool means the legacy unpooled / single-cluster
// default — kept for back-compat with deployments that pre-date multi-pool.
type Pool string

