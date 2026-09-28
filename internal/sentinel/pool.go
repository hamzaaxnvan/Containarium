package sentinel

// Pool is a tag identifying a Containarium cluster ("pool"). Used as a
// routing key by the sentinel's primary registry, peer discovery, and the
// token policy. An empty Pool means the legacy unpooled / single-cluster
// default — kept for back-compat with deployments that pre-date multi-pool.
type Pool string

// PoolAny is a wildcard pool used in TokenPolicy rules to mean "this token
// is authorized for every pool, including the empty/unpooled case." Use
// sparingly; per-pool tokens give you tighter isolation.
const PoolAny Pool = "*"

// String returns the pool name as a plain string. Provided so Pool can be
// used directly with %s, log statements, etc.
func (p Pool) String() string { return string(p) }
