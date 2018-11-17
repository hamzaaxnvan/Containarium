package logframe

import (
	"bytes"
	"testing"
)

func TestEncodeFrame_RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		stream  Stream
		payload []byte
	}{
		{"stdout text", Stdout, []byte("hello world\n")},
		{"stderr text", Stderr, []byte("warning: something\n")},
		{"empty payload", Stdout, []byte{}},
		{"binary payload", Stderr, []byte{0x00, 0xff, 0x10, 0x80}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d Demuxer
			frames, err := d.Write(EncodeFrame(tc.stream, tc.payload))
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("got %d frames, want 1", len(frames))
			}
			if frames[0].Stream != tc.stream {
				t.Errorf("Stream = %v, want %v", frames[0].Stream, tc.stream)
			}
			if !bytes.Equal(frames[0].Payload, tc.payload) {
				t.Errorf("Payload = %q, want %q", frames[0].Payload, tc.payload)
			}
		})
	}
}

func TestDemuxer_BuffersPartialLine(t *testing.T) {
	var d Demuxer
	full := EncodeFrame(Stdout, []byte("hello"))
	// Split the encoded frame mid-line — no trailing newline yet.
	split := len(full) - 3

	frames, err := d.Write(full[:split])
	if err != nil {
		t.Fatalf("Write (partial): %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("got %d frames from a partial line, want 0", len(frames))
	}

	frames, err = d.Write(full[split:])
	if err != nil {
		t.Fatalf("Write (rest): %v", err)
	}
	if len(frames) != 1 || string(frames[0].Payload) != "hello" {
		t.Fatalf("got %+v, want one frame with payload %q", frames, "hello")
	}
}

// TestDemuxer_MultiByteRuneSplitAcrossFrames pins the whole reason framing
// is base64: a UTF-8 rune split across two separate writer chunks must not
// corrupt either frame — each decodes cleanly on its own, and concatenating
// the decoded payloads (the caller's job, not the demuxer's) reconstructs
// the original bytes exactly.
func TestDemuxer_MultiByteRuneSplitAcrossFrames(t *testing.T) {
	original := []byte("héllo") // 'é' is 2 bytes in UTF-8
	splitAt := 2                // lands inside the 2-byte rune

	var d Demuxer
	var got []byte
	for _, chunk := range [][]byte{original[:splitAt], original[splitAt:]} {
		frames, err := d.Write(EncodeFrame(Stdout, chunk))
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		for _, f := range frames {
			got = append(got, f.Payload...)
		}
	}
	if !bytes.Equal(got, original) {
		t.Errorf("reassembled = %q, want %q", got, original)
	}
}

func TestDemuxer_InterleavedStreamsPreserveOrder(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(EncodeFrame(Stdout, []byte("out1")))
	buf.Write(EncodeFrame(Stderr, []byte("err1")))
	buf.Write(EncodeFrame(Stdout, []byte("out2")))
	buf.Write(EncodeFrame(Stderr, []byte("err2")))

	var d Demuxer
	frames, err := d.Write(buf.Bytes())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := []struct {
		stream  Stream
		payload string
	}{
		{Stdout, "out1"}, {Stderr, "err1"}, {Stdout, "out2"}, {Stderr, "err2"},
	}
	if len(frames) != len(want) {
		t.Fatalf("got %d frames, want %d", len(frames), len(want))
	}
	for i, w := range want {
