package backup

import "time"

// LastSuccessByUsername returns, for every tenant with at least one
// stored backup, the CreatedAt of their most recent one. This is the
// durable signal a dead-man-style metrics-export heartbeat reads at
// every export tick (#2294) to report "seconds since last successful
