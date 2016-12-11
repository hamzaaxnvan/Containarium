package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// #1590: ProvisionTLS populated its policy slice only on a 200 and had no
// else-branch, so any other status left the slice nil and control fell through
// to the "no existing policies, create a new one" path. POST to a Caddy admin
// API array path APPENDS, so a transient read failure (500/503, a proxy
// hiccup, the admin API mid-reload) grafted a duplicate policy alongside the
// real ones instead of appending a subject to the existing one.
//
// Duplicate policies aren't cosmetic: Caddy matches the first policy whose
// subjects match, so a duplicate carrying different issuers can shadow the
