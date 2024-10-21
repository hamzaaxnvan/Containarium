package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"filippo.io/age"
)

// DefaultMaxDumpBytes bounds both the stored archive and its plaintext.
// Streaming keeps memory independent of this limit; the limit also bounds
// transfer and staging work for unexpectedly large dumps.
const DefaultMaxDumpBytes int64 = 16 << 30

// WithMaxDumpBytes overrides the 16 GiB archive limit. Non-positive values
// retain the default; there is deliberately no unlimited mode.
func WithMaxDumpBytes(n int64) Option {
	return func(m *Manager) {
		if n > 0 {
			m.maxDumpBytes = n
		}
	}
}

var ErrDumpTooLarge = errors.New("backup dump exceeds size limit")

// boundedReader probes one extra byte at the boundary to distinguish a
// complete stream from a truncated one. In particular, LimitReader alone
// would turn an oversized dump into an apparently successful short dump.
type boundedReader struct {
	mu       sync.Mutex
	r        io.Reader
	limit, n int64
	err      error
	eof      bool
}

func (r *boundedReader) Read(p []byte) (int, error) {
	// HTTP transports may finish a response before their request-body
	// goroutine finishes reading. Keep the completion check race-free.
	r.mu.Lock()
	if len(p) == 0 {
		r.mu.Unlock()
		return 0, nil
	}
	if r.err != nil {
		err := r.err
		r.mu.Unlock()
		return 0, err
	}
	remaining := r.limit - r.n
	r.mu.Unlock()
	// Never hold the state lock across I/O: an early HTTP response must
	// be able to observe an incomplete upload even while Read is blocked.
	if remaining == 0 {
		var extra [1]byte
		n, err := r.r.Read(extra[:])
		if n > 0 {
			err = fmt.Errorf("%w (%d bytes)", ErrDumpTooLarge, r.limit)
		}
		r.mu.Lock()
		if err == io.EOF {
			r.eof = true
		} else if err != nil {
			r.err = err
		}
		r.mu.Unlock()
		return 0, err
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.r.Read(p)
	r.mu.Lock()
	r.n += int64(n)
	if err == io.EOF {
		r.eof = true
	} else if err != nil {
		r.err = err
	}
	r.mu.Unlock()
	return n, err
}

func (r *boundedReader) result() (int64, error, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n, r.err, r.eof
}

type boundedWriter struct {
	w        io.Writer
	limit, n int64
	err      error
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.limit-w.n {
		w.err = fmt.Errorf("%w (%d bytes)", ErrDumpTooLarge, w.limit)
		return 0, w.err
	}
	n, err := w.w.Write(p)
	w.n += int64(n)
	if err != nil {
		w.err = err
	}
	return n, err
}

// stageDump streams src (encrypted to recipients, when any) into the backup
// directory as <id><ext>, bounded by limit on both sides.
func (m *Manager) stageDump(src io.Reader, id, ext string, recipients []string, limit int64) (string, int64, string, error) {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return "", 0, "", err
	}
	f, err := os.CreateTemp(m.dir, ".backup-*.tmp")
	if err != nil {
		return "", 0, "", err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	h := sha256.New()
	dst := &boundedWriter{w: io.MultiWriter(f, h), limit: limit}
	var out io.Writer = dst
	var encrypted io.WriteCloser
	if len(recipients) > 0 {
		rs := make([]age.Recipient, 0, len(recipients))
		for _, recipient := range recipients {
			r, err := parseRecipient(recipient)
			if err != nil {
				return "", 0, "", err
			}
			rs = append(rs, r)
		}
		encrypted, err = age.Encrypt(dst, rs...)
		if err != nil {
			return "", 0, "", fmt.Errorf("encrypt dump: %w", err)
		}
		out = encrypted
	}
	in := &boundedReader{r: src, limit: limit}
	n, err := io.Copy(out, in)
	if err != nil {
		return "", 0, "", fmt.Errorf("stage dump: %w", err)
	}
	if n == 0 {
		return "", 0, "", fmt.Errorf("dump produced an empty archive (backup hook produced no output)")
