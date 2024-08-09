package wake

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/auth"
)

// fakeStarter satisfies WakeStarter for tests.
type fakeStarter struct {
	ready bool
	ip    string
	port  int
	err   error
}

func (f *fakeStarter) WakeForRequest(ctx context.Context, username string) (bool, string, int, error) {
	return f.ready, f.ip, f.port, f.err
}

// fakeLookup satisfies RouteLookup for tests. Returns a static route
// when the incoming Host matches the configured fullDomain.
type fakeLookup struct {
	fullDomain    string
	containerName string
	targetIP      string
	targetPort    int
}

func (f *fakeLookup) ResolveByHost(ctx context.Context, host string) (*app.RouteRecord, bool, error) {
	// http.Request.Host can include ":<port>"; we compare prefix.
	if host == f.fullDomain || strings.HasPrefix(host, f.fullDomain+":") {
		return &app.RouteRecord{
			FullDomain:    f.fullDomain,
			Subdomain:     f.fullDomain,
			ContainerName: f.containerName,
