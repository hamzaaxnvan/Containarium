package sentinel

import (
	"bufio"
	"bytes"
	"net"
	"testing"

	proxyproto "github.com/pires/go-proxyproto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteProxyV2_IPv4(t *testing.T) {
	src := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 54321}
	dst := &net.TCPAddr{IP: net.IPv4(198, 51, 100, 1), Port: 443}

	var buf bytes.Buffer
	n, err := WriteProxyV2(&buf, src, dst)
	require.NoError(t, err)
	assert.Equal(t, 28, n, "IPv4 PROXY v2 header should be 28 bytes")
	assert.Equal(t, 28, buf.Len())

	// Signature
	assert.Equal(t, proxyV2Sig[:], buf.Bytes()[0:12])
	// ver+cmd = 0x21
	assert.Equal(t, byte(0x21), buf.Bytes()[12])
	// fam+proto = 0x11 (IPv4 + STREAM)
	assert.Equal(t, byte(0x11), buf.Bytes()[13])
	// body length = 12
	assert.Equal(t, byte(0x00), buf.Bytes()[14])
	assert.Equal(t, byte(0x0c), buf.Bytes()[15])
	// Source IP
	assert.Equal(t, []byte{203, 0, 113, 7}, buf.Bytes()[16:20])
	// Dest IP
	assert.Equal(t, []byte{198, 51, 100, 1}, buf.Bytes()[20:24])
	// Source port (54321)
	assert.Equal(t, []byte{0xd4, 0x31}, buf.Bytes()[24:26])
	// Dest port (443)
	assert.Equal(t, []byte{0x01, 0xbb}, buf.Bytes()[26:28])

	// Roundtrip via the standard parser as an oracle.
	hdr, err := proxyproto.Read(bufio.NewReader(&buf))
	require.NoError(t, err)
	require.NotNil(t, hdr)
	assert.Equal(t, byte(2), hdr.Version)
	assert.Equal(t, proxyproto.PROXY, hdr.Command)
	assert.Equal(t, src.String(), hdr.SourceAddr.String())
	assert.Equal(t, dst.String(), hdr.DestinationAddr.String())
}

func TestWriteProxyV2_IPv6(t *testing.T) {
	src := &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 54321}
	dst := &net.TCPAddr{IP: net.ParseIP("2001:db8::2"), Port: 443}

	var buf bytes.Buffer
	n, err := WriteProxyV2(&buf, src, dst)
	require.NoError(t, err)
	assert.Equal(t, 52, n, "IPv6 PROXY v2 header should be 52 bytes")

