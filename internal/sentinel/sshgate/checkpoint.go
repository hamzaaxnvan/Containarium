package sshgate

import (
	"errors"
	"sort"
	"time"
)

// Risk is a conservative jail-window charge, including a pending setup deadline.
type Risk struct {
	At   time.Time
	Cost int
}

// State is bounded restart state; it contains no live lease or socket authority.
type State struct {
	Key     Key
	Route   uint64
	Next    time.Time
	Delay   time.Duration
	Blocked bool
