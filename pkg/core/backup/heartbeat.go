package backup

import "time"

// LastSuccessByUsername returns, for every tenant with at least one
// stored backup, the CreatedAt of their most recent one. This is the
// durable signal a dead-man-style metrics-export heartbeat reads at
// every export tick (#2294) to report "seconds since last successful
// backup" per tenant — a value that survives a daemon restart, because
// it comes straight from the existing on-disk backup index (the same
// one List/Get already serve from) rather than any new in-memory
// bookkeeping tied to the daemon process's own lifetime.
//
// A tenant with zero stored backups is simply absent from the returned
// map — there is nothing to report an age against, and a zero-value
// time.Time would read as "last success in year 1", which is a worse
