package sentinel

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandshakeDecoderOverReads is the QA reproducer for Bug A: when the
// peer side reads the handshake response with json.NewDecoder over a stream
// that has additional bytes appended (e.g., a yamux SYN frame written by
// the sentinel right after the JSON), the decoder's internal buffer
// swallows those extra bytes. They never reach yamux.Server, and yamux
// then misaligns its frame reader.
//
// This test FAILS on the buggy code path (json.NewDecoder.Decode) and
// PASSES on the line-delimited fix (readJSONLine). Keep both forms here
// so future changes can't silently regress.
func TestHandshakeDecoderOverReads(t *testing.T) {
	jsonResponse := `{"ok":true,"assigned_ip":"127.0.0.7"}` + "\n"
	yamuxSYN := []byte{
		0x00,       // version 0
		0x00,       // type SYN
		0x00, 0x01, // flags = SYN
		0x00, 0x00, 0x00, 0x01, // stream ID 1
		0x00, 0x00, 0x00, 0x00, // length 0
	}

	// Simulate the wire: JSON response immediately followed by a yamux
	// SYN frame, both in one io.Reader. This is what the spot sees when
	// the sentinel writes the response and immediately initiates keysync.
	combined := bytes.NewReader(append([]byte(jsonResponse), yamuxSYN...))

	t.Run("buggy: json.Decoder over-reads", func(t *testing.T) {
		r := bytes.NewReader(append([]byte(jsonResponse), yamuxSYN...))
		dec := json.NewDecoder(r)
		var resp TunnelHandshakeResponse
		require.NoError(t, dec.Decode(&resp))
		assert.True(t, resp.OK)

		// dec.Buffered() returns whatever the decoder pre-fetched but did
		// not consume. If yamux frame bytes are in here, they are LOST
		// when we hand the underlying reader to yamux.
