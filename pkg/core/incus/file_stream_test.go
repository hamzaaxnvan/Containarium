package incus

import (
	"errors"
	"io"
	"strings"
	"testing"

	incusclient "github.com/lxc/incus/v7/client"
)

type trackedFileStream struct {
	io.Reader
	reads  int
	closed bool
}

func (r *trackedFileStream) Read(p []byte) (int, error) { r.reads++; return r.Reader.Read(p) }
func (r *trackedFileStream) Close() error               { r.closed = true; return nil }

type streamFileServer struct {
	incusclient.InstanceServer
	body    *trackedFileStream
	replay  bool
	written int64
	mode    int
}

func (s *streamFileServer) GetInstanceFile(string, string) (io.ReadCloser, *incusclient.InstanceFileResponse, error) {
	return s.body, &incusclient.InstanceFileResponse{Type: "file"}, nil
}
func (s *streamFileServer) CreateInstanceFile(_, _ string, args incusclient.InstanceFileArgs) error {
	if s.replay {
		if _, err := args.Content.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	n, err := io.Copy(io.Discard, args.Content)
	s.written, s.mode = n, args.Mode
	return err
}

func TestReadFileStream_TransfersUnreadBodyOwnership(t *testing.T) {
	body := &trackedFileStream{Reader: strings.NewReader("dump bytes")}
	c := &Client{server: &streamFileServer{body: body}}
	r, err := c.ReadFileStream("tenant-container", "/tmp/dump")
	if err != nil {
		t.Fatal(err)
	}
	if body.reads != 0 || body.closed {
		t.Fatal("file was read or closed before caller consumed it")
