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
	Risks   []Risk
}

// Checkpoint converts pending reservations to worst-case deadline charges.
func (g *Gate) Checkpoint() []State {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	rows := make([]State, 0, len(g.entries))
	for key, e := range g.entries {
		g.reap(e, now)
		s := State{Key: key, Route: e.route, Next: e.next, Delay: e.delay, Blocked: e.blocked}
		for _, c := range e.charges {
			s.Risks = append(s.Risks, Risk{At: c.at, Cost: c.cost})
		}
		for _, r := range e.pending {
			s.Risks = append(s.Risks, Risk{At: r.lease.Deadline, Cost: r.cost})
			s.Blocked = true
			if s.Delay == 0 {
				s.Delay = g.policy.InitialDelay
			}
