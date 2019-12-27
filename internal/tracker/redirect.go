package tracker

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrCrossOriginRedirect is returned when a provider client is asked to
// follow a redirect to a different scheme or host. Following it could send
// the connection credential to an endpoint the operator did not configure.
var ErrCrossOriginRedirect = errors.New("refusing to follow HTTP redirect to a different origin")

// WithSameOriginRedirects returns a shallow copy of client whose redirects
// are restricted to the original scheme and host. The caller's client is not
// mutated; its transport, timeout, cookie jar, and existing same-origin
// redirect policy are preserved.
func WithSameOriginRedirects(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	previous := clone.CheckRedirect
