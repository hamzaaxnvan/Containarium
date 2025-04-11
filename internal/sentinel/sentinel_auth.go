package sentinel

import (
	"crypto/ed25519"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/config"
)

// Sentinel-to-daemon authentication.
//
// Several daemon endpoints (/certs, /authorized-keys,
// /authorized-keys/sentinel) are now HMAC-gated. The shared secret
// lives in CONTAINARIUM_SENTINEL_AUTH_SECRET on both ends. This file
// is the sentinel side: a small helper that builds an HTTP request,
// stamps the auth headers, and returns it. Callers (keysync,
// certsync) replace bare client.Get / client.Post with
// client.Do(newSignedRequest(...)).
