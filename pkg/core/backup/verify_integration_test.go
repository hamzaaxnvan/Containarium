//go:build integration

// Integration coverage for the restore test (#1159), run against real
// Postgres containers rather than the in-memory fake.
//
// The unit tests in verify_test.go pin the orchestration: what gets
// created, what gets dropped, what is refused. They cannot show that the
// dump a real pg_dump produces is loadable by a real pg_restore, which is
// the entire claim `backup verify` makes. This file does.
//
//	go test -tags=integration ./pkg/core/backup/ -run TestIntegrationVerify -v
//
// Requires podman (or docker via CONTAINARIUM_TEST_RUNTIME=docker) on
// PATH. Containers are disposable and removed on teardown.
package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// podmanOps implements ContainerOps against real containers, so the
// manager under test runs the same pg_dump / pg_restore / psql commands
// it would run in production.
type podmanOps struct {
	t       *testing.T
	runtime string
}

func (p *podmanOps) run(container string, stdin []byte, command []string) (string, string, error) {
	args := []string{"exec"}
	if stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, container)
	args = append(args, command...)

	cmd := exec.Command(p.runtime, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (p *podmanOps) Exec(container string, command []string) error {
	_, _, err := p.run(container, nil, command)
	return err
}

func (p *podmanOps) ExecWithOutput(container string, command []string) (string, string, error) {
	return p.run(container, nil, command)
}

func (p *podmanOps) ReadFile(container, path string) ([]byte, error) {
	cmd := exec.Command(p.runtime, "exec", container, "cat", path)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (p *podmanOps) WriteFile(container, path string, content []byte, _ string) error {
	_, stderr, err := p.run(container, content, []string{"sh", "-c", "cat > " + path})
	if err != nil {
		p.t.Logf("WriteFile stderr: %s", stderr)
	}
	return err
}

// commandDump waits for cat at EOF so a failed container read cannot be
// mistaken for a successfully completed stream.
type commandDump struct {
	io.ReadCloser
	cmd    *exec.Cmd
	waited bool
}

func (r *commandDump) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err == io.EOF && !r.waited {
		r.waited = true
		if waitErr := r.cmd.Wait(); waitErr != nil {
			return n, waitErr
		}
	}
	return n, err
}

func (r *commandDump) Close() error {
	err := r.ReadCloser.Close()
	if !r.waited {
		_ = r.cmd.Process.Kill()
		r.waited = true
		_ = r.cmd.Wait()
	}
	return err
}

func (p *podmanOps) ReadFileStream(container, path string) (io.ReadCloser, error) {
	cmd := exec.Command(p.runtime, "exec", container, "cat", path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = out.Close()
		return nil, err
	}
	return &commandDump{ReadCloser: out, cmd: cmd}, nil
}

func (p *podmanOps) WriteFileStream(container, path string, content io.Reader, _ string) error {
	cmd := exec.Command(p.runtime, "exec", "-i", container, "sh", "-c", "umask 077; cat > "+shellQuote(path))
	cmd.Stdin = content
	return cmd.Run()
}

func runtimeBin(t *testing.T) string {
	bin := os.Getenv("CONTAINARIUM_TEST_RUNTIME")
	if bin == "" {
		bin = "podman"
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%s not on PATH; skipping integration test", bin)
	}
	return bin
}

// startPostgres launches a disposable Postgres and waits for it to accept
// connections. Registers its own teardown.
func startPostgres(t *testing.T, rt, name string) {
	t.Helper()
	_ = exec.Command(rt, "rm", "-f", name).Run()

	out, err := exec.Command(rt, "run", "-d", "--name", name,
		"-e", "POSTGRES_HOST_AUTH_METHOD=trust",
		"-e", "POSTGRES_PASSWORD=unused",
		"docker.io/library/postgres:16-alpine").CombinedOutput()
	if err != nil {
		t.Skipf("could not start %s (%v): %s", name, err, out)
	}
	t.Cleanup(func() { _ = exec.Command(rt, "rm", "-f", name).Run() })

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if err := exec.Command(rt, "exec", name, "pg_isready", "-U", "postgres").Run(); err == nil {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s never became ready", name)
}

// TestIntegrationVerifyAgainstRealPostgres is the end-to-end claim: a
// dump taken from a real database restores into a throwaway target and
// passes its checks, and the source database is left untouched.
func TestIntegrationVerifyAgainstRealPostgres(t *testing.T) {
	rt := runtimeBin(t)
	const src, tgt = "containarium-verify-src", "containarium-verify-tgt"
	startPostgres(t, rt, src)
	startPostgres(t, rt, tgt)

	ops := &podmanOps{t: t, runtime: rt}

	// Seed the source with a table and a known row count.
	seed := `CREATE DATABASE app;
\c app
CREATE TABLE widgets (id serial PRIMARY KEY, name text);
INSERT INTO widgets (name) SELECT 'w' || g FROM generate_series(1,25) g;`
	if _, stderr, err := ops.run(src, []byte(seed), []string{"psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1"}); err != nil {
		t.Fatalf("seed source: %v: %s", err, stderr)
	}

	m := NewManager(ops, nil, t.TempDir())
	rec, err := m.Create(CreateOptions{
		Username:      "alice",
		ContainerName: src,
