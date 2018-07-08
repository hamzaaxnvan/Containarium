package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/gorilla/websocket"
	"golang.org/x/term"
)

// detachEscape is a byte-scanning io.Reader that watches for Incus's own
// detach sequence — <ctrl>+a then 'q' — so `containarium console` behaves
// the same way `incus console` does for anyone already used to it. Closing
// detach signals a clean detach without killing the target instance.
type detachEscape struct {
	r          io.Reader
	detach     chan struct{}
	once       sync.Once
	foundCtrlA bool
}

func newDetachEscape(r io.Reader) (*detachEscape, <-chan struct{}) {
	ch := make(chan struct{})
	return &detachEscape{r: r, detach: ch}, ch
}

func (d *detachEscape) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	for _, b := range p[:n] {
		switch {
		case b == '\x01': // <ctrl>+a
			d.foundCtrlA = true
		case b == 'q' && d.foundCtrlA:
			d.once.Do(func() { close(d.detach) })
			d.foundCtrlA = false
		default:
			d.foundCtrlA = false
		}
	}
	return n, err
}

// attachConsole opens a live, bidirectional connection to a VM instance's
// serial console. Unlike --log, this requires --http with an http(s)://
// --server: it needs a WebSocket upgrade, which a plain gRPC endpoint has
// no path for.
func attachConsole(username string) error {
	if !httpMode || serverAddr == "" {
		return fmt.Errorf("live console attach requires --http with an http(s):// --server address (the ring-buffer log via --log works over gRPC too)")
	}

	wsURL, err := consoleWebSocketURL(serverAddr, username)
	if err != nil {
		return err
	}

	headers := http.Header{}
	headers.Set("Sec-WebSocket-Protocol", auth.WSSubprotocolBearer+", "+authToken)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return fmt.Errorf("failed to attach console (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("failed to attach console: %w", err)
	}
	defer func() { _ = conn.Close() }()

	stdinFd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return fmt.Errorf("failed to set raw terminal mode: %w", err)
	}
	defer func() { _ = term.Restore(stdinFd, oldState) }()

	fmt.Print("To detach from the console, press: <ctrl>+a q\r\n")

	stdin, detach := newDetachEscape(os.Stdin)
	done := make(chan struct{})

	// stdin -> console
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				if writeErr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					close(done)
					return
				}
			}
			if err != nil {
				close(done)
				return
			}
		}
	}()
