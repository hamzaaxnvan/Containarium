package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

type anonDoorProbe struct {
	jwtCalled, innerCalled bool
	username               string
	scopes                 []string
	roles                  []string
}

func (p *anonDoorProbe) handlers() (jwt, inner http.Handler) {
	jwt = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { p.jwtCalled = true; w.WriteHeader(http.StatusTeapot) })
	inner = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.innerCalled = true
		p.username, _ = auth.UsernameFromContext(r.Context())
		p.scopes, _ = auth.ScopesFromContext(r.Context())
		p.roles, _ = auth.RolesFromContext(r.Context())
