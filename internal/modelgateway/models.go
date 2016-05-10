package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Upstream model listing (#1726, the ListGatewayModels RPC).
//
// A caller that is about to point an engine at the gateway wants to know which
// models it may ask for — and the honest answer is "whatever the key this
// gateway would spend can see". So this reads the provider's own model list
// through the SAME key resolution the proxy path uses (resolveKey), rather than
// returning a list compiled into the daemon: the list a caller gets is the list
// its calls will actually be able to use.
//
// The caller never holds a provider key. It names an owner and a provider; the
// key is resolved here and never leaves this process, exactly as on the data
// plane.

var (
	// ErrUnknownProvider means the daemon serves no provider by that name —
	// either it was never compiled in, or no operator registered its upstream.
	ErrUnknownProvider = errors.New("modelgateway: unknown provider")
	// ErrNoKey means neither the key owner nor the daemon has a key for this
	// provider, so there is nothing to spend on the lookup.
	ErrNoKey = errors.New("modelgateway: no key for this owner or provider")
	// ErrModelListUnsupported means the provider is not OpenAI-shaped and so has
	// no /v1/models contract to read. Returned rather than an empty list, which
	// would read as "this provider offers no models".
	ErrModelListUnsupported = errors.New("modelgateway: provider has no OpenAI-shaped model list")
)

// UpstreamStatusError is a non-2xx answer from the provider. It carries the
// status code so the caller can map it (401 from upstream is a bad customer key,
// not an internal failure) and a trimmed body for the operator-facing message.
type UpstreamStatusError struct {
	Provider   string
	StatusCode int
	Body       string
}

func (e *UpstreamStatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("modelgateway: %s model list: upstream returned %d", e.Provider, e.StatusCode)
	}
	return fmt.Sprintf("modelgateway: %s model list: upstream returned %d: %s", e.Provider, e.StatusCode, e.Body)
}
