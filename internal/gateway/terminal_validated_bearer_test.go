package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/gorilla/websocket"
)

// fakeWSJWT is shaped like a JWT so the subprotocol parser accepts it. It is
// never validated: whether the bearer counts as validated is the table's
// "validated" column, standing in for the gateway wrapper's ValidateToken.
const fakeWSJWT = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.c2lnbmF0dXJlLXRlc3Q"

// TestTerminalUpgrader_OriginRuleOverTheWire dials the real upgrader with a
// real client. The no-Origin exemption must follow a bearer the wrapper
// validated, never a bearer that is merely present on the request.
func TestTerminalUpgrader_OriginRuleOverTheWire(t *testing.T) {
	type bearerForm int
	const (
		noBearer bearerForm = iota
		subprotocolBearer
		headerBearer
		queryBearer
	)

	tests := []struct {
		name       string
		envOrigins string
