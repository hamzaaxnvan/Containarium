package coderun

import (
	"bytes"
	"testing"

	"github.com/footprintai/containarium/internal/logframe"
)

func TestDemuxWriter_RoutesEachStream(t *testing.T) {
	var stdout, stderr bytes.Buffer
	w := NewDemuxWriter(&stdout, &stderr)

	var framed bytes.Buffer
	framed.Write(logframe.EncodeFrame(logframe.Stdout, []byte("out-a")))
	framed.Write(logframe.EncodeFrame(logframe.Stderr, []byte("err-a")))
	framed.Write(logframe.EncodeFrame(logframe.Stdout, []byte("out-b")))

	if _, err := w.Write(framed.Bytes()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if stdout.String() != "out-aout-b" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "out-aout-b")
	}
	if stderr.String() != "err-a" {
		t.Errorf("stderr = %q, want %q", stderr.String(), "err-a")
