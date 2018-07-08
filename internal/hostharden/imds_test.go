package hostharden

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type call struct {
	name string
	args []string
}

func fakeRunner(t *testing.T, calls *[]call, responses map[string]result) runner {
	t.Helper()
	return func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, call{name, args})
		key := name + " " + strings.Join(args, " ")
		for pattern, r := range responses {
			if strings.HasPrefix(key, pattern) {
				return []byte(r.out), r.err
			}
		}
		t.Fatalf("unexpected command: %s", key)
		return nil, nil
	}
}

type result struct {
	out string
	err error
}

func TestBridgeSubnet(t *testing.T) {
	t.Run("resolves the configured address", func(t *testing.T) {
		var calls []call
		run := fakeRunner(t, &calls, map[string]result{
			"incus network get incusbr0 ipv4.address": {out: "10.0.3.1/24\n"},
		})
		got, err := bridgeSubnet(run, "incusbr0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "10.0.3.1/24" {
			t.Errorf("got %q, want 10.0.3.1/24", got)
		}
	})

	t.Run("errors when incus fails", func(t *testing.T) {
		var calls []call
		run := fakeRunner(t, &calls, map[string]result{
			"incus network get incusbr0 ipv4.address": {out: "not found", err: errors.New("exit 1")},
		})
		if _, err := bridgeSubnet(run, "incusbr0"); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("errors when the bridge has no address (none)", func(t *testing.T) {
		var calls []call
		run := fakeRunner(t, &calls, map[string]result{
			"incus network get incusbr0 ipv4.address": {out: "none\n"},
		})
		if _, err := bridgeSubnet(run, "incusbr0"); err == nil {
			t.Fatal("expected an error for an unconfigured bridge")
