package tracker

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Scope routes (#2021): (tenant, connection, scope) -> skill id. The
// dispatcher (#2022) starts a run only for a `scope:<scope>` label that
// has a route here; an unmapped scope is never dispatched. See
// docs/architecture/issue-triggered-agents.md.

// ScopeLabelPrefix is the label prefix a route's Scope is the suffix of:
// the route with Scope "product" matches the label "scope:product".
const ScopeLabelPrefix = "scope:"

// IsScopeLabel reports whether label is a scope:<role> label — the kind
// the dispatcher routes on — compared after trimming and with the prefix
// matched case-insensitively (GitHub label names are case-insensitive,
// so "Scope:product" can land as the existing "scope:product" label).
// Used to lineage-bind a run token's scope writes (#2060); deliberately
// broader than "has a route", since a route can be added later.
func IsScopeLabel(label string) bool {
	label = strings.TrimSpace(label)
	return len(label) >= len(ScopeLabelPrefix) && strings.EqualFold(label[:len(ScopeLabelPrefix)], ScopeLabelPrefix)
}

// Route is the storage-layer view of a TrackerRoute.
type Route struct {
	Username   string
	Connection string
	Scope      string
	SkillID    string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ErrRouteNotFound is returned by GetRoute / DeleteRoute when the
// (username, connection, scope) tuple has no row. SetRoute against a
// connection that does not exist returns ErrNotFound (the connection's
// error), not this one.
var ErrRouteNotFound = errors.New("tracker: route not found")

// routeScopeRe bounds a scope to what both GitHub and GitLab accept in a
// label and what a human would type after "scope:": no whitespace, no
// colon or slash, no leading punctuation, at most 64 characters.
var routeScopeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateRouteScope rejects a scope that could never match a label as a
// route suffix. The full label ("scope:product") gets a pointed error,
// since it is the likeliest mistake.
func ValidateRouteScope(scope string) error {
	if scope == "" {
		return errors.New("tracker: scope is required")
	}
	if strings.HasPrefix(scope, ScopeLabelPrefix) {
		return fmt.Errorf("tracker: scope must be the label suffix without %q (use %q)", ScopeLabelPrefix, strings.TrimPrefix(scope, ScopeLabelPrefix))
