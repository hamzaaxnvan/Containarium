package sentinel

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// CertScope is the set of domains one backend is registered for. A
// backend's certificate sync may only supply certificates inside its scope.
//
// For a tunnel backend the scope is its primary registration (the public
// hostname, aliases and base domains declared in the tunnel handshake). For
// the in-fleet backend it is the --backend-hostname, --backend-alias and
// --backend-base-domain flags. A backend with an empty scope supplies no
// certificates.
type CertScope struct {
	Hostname    string
	Aliases     []string
	BaseDomains []string
}

// Empty reports whether the scope names no domain at all.
func (s CertScope) Empty() bool {
	return s.Hostname == "" && len(s.Aliases) == 0 && len(s.BaseDomains) == 0
}

// matchExact is the match strength of a certificate domain equal to the
// scope's hostname or one of its aliases. It is larger than any base-domain
// match, whose strength is the base domain's length (at most 253).
const matchExact = 1 << 16

// match reports how strongly domain (a certificate's domain, possibly a
// "*." wildcard) falls inside the scope: 0 when it is outside, matchExact
// for the hostname or an alias, and len(base) for a base domain — the base
// domain itself, its wildcard, or any name under it. When several base
// domains cover domain the longest one counts.
func (s CertScope) match(domain string) int {
	domain = strings.ToLower(domain)
	if domain == "" {
		return 0
	}
	if domain == s.Hostname {
		return matchExact
	}
	for _, a := range s.Aliases {
		if domain == a {
			return matchExact
		}
	}
	name := strings.TrimPrefix(domain, "*.")
	best := 0
	for _, bd := range s.BaseDomains {
		if bd == "" {
			continue
		}
		if (name == bd || strings.HasSuffix(name, "."+bd)) && len(bd) > best {
			best = len(bd)
		}
	}
	return best
}

// Validate checks every name in the scope is a lowercase DNS name with at
// least two labels and no wildcard. An empty scope is valid.
