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
