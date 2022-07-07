package sentinel

import (
	"bufio"
	"errors"
	"net"
)

// errNotTLS indicates the connection's first bytes are not a TLS handshake.
var errNotTLS = errors.New("not a TLS handshake")

// errNoSNI indicates a valid TLS handshake without a server_name extension.
var errNoSNI = errors.New("no SNI in ClientHello")

// peekSNI reads enough of a TLS ClientHello from conn to extract SNI without
// consuming the bytes. It returns the SNI hostname and a connection that
// transparently replays the peeked bytes followed by the rest of the original
// connection. The returned net.Conn must be used in place of conn for any
// further reads.
//
// If the peek fails (not TLS, malformed, or no SNI), an error is returned but
// the returned net.Conn is still usable — callers can fall through to a
// non-SNI-based forwarding path.
func peekSNI(conn net.Conn) (string, net.Conn, error) {
	full, wrapped, err := peekHelloRecord(conn)
	if err != nil {
		return "", wrapped, err
	}
	sni, err := extractSNI(full)
	return sni, wrapped, err
}

// clientHello holds the fields the sentinel mux reads from a peeked TLS
// ClientHello.
type clientHello struct {
	// SNI is the server_name host_name value, or "" when the extension is
	// absent.
	SNI string
	// ALPN is the application_layer_protocol_negotiation protocol list in
	// the order offered, or nil when the extension is absent.
	ALPN []string
}

// peekClientHello is like peekSNI but also returns the ALPN protocol list.
// A ClientHello without SNI or ALPN is not an error; the corresponding field
// is simply empty. The returned net.Conn replays the peeked bytes and must be
// used in place of conn, including on error.
func peekClientHello(conn net.Conn) (clientHello, net.Conn, error) {
	full, wrapped, err := peekHelloRecord(conn)
	if err != nil {
		return clientHello{}, wrapped, err
	}
	hello, err := parseClientHello(full)
	return hello, wrapped, err
}

// maxHelloRecord is the largest TLS record the peek helpers buffer:
// 5-byte header plus a 16384-byte body.
const maxHelloRecord = 16389

// peekHelloRecord peeks the first TLS record from conn without consuming it.
func peekHelloRecord(conn net.Conn) ([]byte, net.Conn, error) {
	br := bufio.NewReaderSize(conn, maxHelloRecord)
	wrapped := &peekConn{Conn: conn, r: br}
	full, err := peekHelloRecordFrom(br)
	return full, wrapped, err
}

// peekHelloRecordFrom peeks the first TLS record from br without consuming
// it. br must have been created with at least maxHelloRecord of buffer.
func peekHelloRecordFrom(br *bufio.Reader) ([]byte, error) {
	// Peek the 5-byte record header to learn record length.
	hdr, err := br.Peek(5)
	if err != nil || len(hdr) < 5 {
		return nil, errNotTLS
	}
	if hdr[0] != 0x16 { // TLS handshake content type
		return nil, errNotTLS
	}
	recLen := int(hdr[3])<<8 | int(hdr[4])
	total := 5 + recLen
	if total < 5 || total > maxHelloRecord {
		return nil, errNotTLS
	}

	full, err := br.Peek(total)
	if err != nil || len(full) < total {
		return nil, errNotTLS
	}
	return full, nil
}

// extractSNI parses a TLS ClientHello (record-framed, starting at byte 0)
// and returns the SNI host_name extension value. It performs strict bounds
// checks at every step so a malformed handshake returns an error rather than
// panicking on slice bounds.
func extractSNI(buf []byte) (string, error) {
	hello, err := parseHello(buf, false)
	return hello.SNI, err
}

// parseClientHello parses the SNI and ALPN extensions of a record-framed
// ClientHello. A malformed extension yields an error; absent extensions do
// not.
func parseClientHello(buf []byte) (clientHello, error) {
	return parseHello(buf, true)
}

// parseHello is the shared ClientHello walker. With withALPN false it keeps
// the original SNI-only contract: it returns as soon as the SNI extension is
// found (ignoring anything after it) and reports errNoSNI when absent. With
// withALPN true it visits every extension and a missing SNI is not an error.
func parseHello(buf []byte, withALPN bool) (clientHello, error) {
	var hello clientHello
	// Record header: type(1) + version(2) + length(2)
	if len(buf) < 5 || buf[0] != 0x16 {
		return hello, errNotTLS
	}
	recLen := int(buf[3])<<8 | int(buf[4])
	if len(buf) < 5+recLen {
		return hello, errors.New("record body truncated")
	}
	body := buf[5 : 5+recLen]

	// Handshake: type(1) + length(3) + body
	if len(body) < 4 || body[0] != 0x01 {
		return hello, errors.New("not a ClientHello")
	}
