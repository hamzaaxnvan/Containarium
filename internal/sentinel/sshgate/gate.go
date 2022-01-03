// Package sshgate provides bounded, shared admission for backend SSH setup.
package sshgate

import (
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

// Key identifies the stable backend and its backend-visible source/egress group.
type Key struct{ Backend, Egress string }

// Policy contains explicit operator-profile limits, not inferred jail defaults.
type Policy struct {
	InitialDelay, MaxDelay, Window, LeaseTimeout time.Duration
	Budget, MaxBackends, MaxPending              int
	Jitter                                       float64
}

// Outcome classifies verified setup results; ordinary session closure is absent.
type Outcome uint8

const (
	// SetupSucceeded means SSH authentication/setup completed, not TCP readiness.
	SetupSucceeded Outcome = iota + 1
	// DialRefused means verified TCP refusal before backend SSH could run.
	DialRefused
	// SetupFailed conservatively retains risk for an unclassified setup failure.
	SetupFailed
	// ProxyAuthRejected identifies rejection of the configured proxy credential.
	ProxyAuthRejected
	// ClientAuthRejected retains jail risk without declaring a healthy backend down.
	ClientAuthRejected
)

// Lease binds one admitted setup to its route, incident and absolute deadline.
type Lease struct {
	Key       Key
	ID, Route uint64
	Deadline  time.Time
	Probe     bool
	epoch     uint64
	owner     *Gate
}

// Reason distinguishes admission failures without parsing error messages.
type Reason uint8

const (
	UnknownBackend Reason = iota + 1
	StaleRoute
	InvalidCost
	Cooldown
	ProbeBusy
	BudgetExhausted
	Capacity
)

// Denied describes a prompt rejection and its conservative retry deadline.
type Denied struct {
	Reason  Reason
	RetryAt time.Time
}

