package server

import (
	"context"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/secrets"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/container"
)

// #1190: on K8s, `secret set` succeeded and the value never reached the box.
// These cover the translation from stored rows to what the box mounts —
// the step where a delivery mode can be silently dropped.

type fakeApplier struct {
	data map[string][]byte
	err  error
}

func (f *fakeApplier) ApplyTenantSecrets(_ context.Context, _ string, data map[string][]byte) error {
	if f.err != nil {
		return f.err
	}
	f.data = data
	return nil
}

// deliverK8s runs the K8s translation over a fixed set of stored rows,
// without a database.
func deliverK8s(t *testing.T, rows map[string]secrets.SecretValue) *fakeApplier {
	t.Helper()
	applier := &fakeApplier{}
	if err := applier.ApplyTenantSecrets(context.Background(), "alice", secretsToK8sData(rows)); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	return applier
}

func TestK8sDelivery_EnvAndFileRowsBothBecomeFiles(t *testing.T) {
	applier := deliverK8s(t, map[string]secrets.SecretValue{
		"API_TOKEN": {Value: "tok", Delivery: secrets.DeliveryEnv},
		"TLS_KEY":   {Value: "pem", Delivery: secrets.DeliveryFile},
	})

	if string(applier.data["API_TOKEN"]) != "tok" {
		t.Errorf("env-mode secret = %q, want its value — an env row dropped here is exactly the "+
			"silent non-delivery #1190 is about", applier.data["API_TOKEN"])
	}
	if string(applier.data["TLS_KEY"]) != "pem" {
		t.Errorf("file-mode secret = %q, want its value", applier.data["TLS_KEY"])
	}
}

// Compose rows are consumed as one dotenv file. Carrying them across as
// individual keys, or dropping them, would each break a compose app.
func TestK8sDelivery_ComposeRowsBecomeOneDotenvFile(t *testing.T) {
	applier := deliverK8s(t, map[string]secrets.SecretValue{
		"DB_PASS": {Value: "p1", Delivery: secrets.DeliveryCompose},
		"DB_USER": {Value: "u1", Delivery: secrets.DeliveryCompose},
	})

	dotenv, ok := applier.data[composeSecretKey]
	if !ok {
		t.Fatalf("no compose dotenv delivered; keys=%v — a compose app would come up with no "+
			"credentials and no error", keysOf(applier.data))
	}
	body := string(dotenv)
	if !strings.Contains(body, "DB_USER=u1") || !strings.Contains(body, "DB_PASS=p1") {
		t.Errorf("dotenv is missing values:\n%s", body)
	}
	// Individual keys too would put the same secret in the box twice, under
	// two names, and only one of them documented.
	if _, dup := applier.data["DB_PASS"]; dup {
		t.Error("a compose row was also delivered as its own file")
	}
}

// The rendered dotenv must be byte-identical between deliveries, or every
// pass looks like a change and rewrites the Secret.
func TestK8sDelivery_DotenvIsStableAcrossDeliveries(t *testing.T) {
	rows := map[string]secrets.SecretValue{
