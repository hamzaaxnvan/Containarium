package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Channel is a user's ntfy destination. The token is never part of it: the
// daemon keeps it as a broker-only tenant secret named by TokenSecret and
// passes it to Send separately.
type Channel struct {
	Username string
	Server   string
	Topic    string
	// TokenSecret names the broker-only secret holding the channel's token;
	// empty for a channel without one. Each token gets a fresh name
	// (NewTokenSecretName) that is never rewritten, so a channel row and the
	// secret it names always belong together: replacing a token is a single
	// write of the row that points at the new secret.
	TokenSecret string
	// RedactCommand sends the tool name only, never its command.
	RedactCommand bool
	UpdatedAt     time.Time
}

// HasToken says a token is stored for the channel.
func (c Channel) HasToken() bool { return c.TokenSecret != "" }

// TokenSecretPrefix starts the name of every secret holding a channel token.
const TokenSecretPrefix = "CONTAINARIUM_NOTIFY_NTFY_TOKEN_"

// NewTokenSecretName returns a fresh secret name for a channel token.
func NewTokenSecretName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("notify: token secret name: %w", err)
	}
	return TokenSecretPrefix + strings.ToUpper(hex.EncodeToString(b[:])), nil
}

// topicPattern is ntfy's own topic rule.
var topicPattern = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// ValidateChannel checks a channel before it is stored. The server must be a
// bare http(s) URL (a path prefix is allowed, for ntfy behind a reverse
// proxy); with a token it must be https, so the token never crosses the
// network in clear.
func ValidateChannel(ch Channel, hasToken bool) error {
	u, err := url.Parse(strings.TrimSpace(ch.Server))
	if err != nil || ch.Server == "" {
		return fmt.Errorf("server must be an http(s) URL, got %q", ch.Server)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("server must be an http(s) URL, got %q", ch.Server)
	case u.Host == "":
		return fmt.Errorf("server %q has no host", ch.Server)
	case u.User != nil:
		return errors.New("server must not carry credentials; pass the token with --token-file or --token-stdin")
	case u.RawQuery != "" || u.Fragment != "" || u.Opaque != "":
		return fmt.Errorf("server %q must not have a query or fragment", ch.Server)
	case hasToken && u.Scheme != "https":
		return errors.New("a channel with a token needs an https server, so the token is never sent in clear")
	}
	if !topicPattern.MatchString(ch.Topic) {
		return fmt.Errorf("topic must be 1-64 letters, digits, '-' or '_', got %q", ch.Topic)
	}
	return nil
}

// Sender delivers messages to ntfy.
