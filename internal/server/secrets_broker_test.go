package server

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestGetSecretRPC_BrokerRow_EmptyValueEvenWithSecretsRead pins the
// server-level half of the write-only guarantee: a caller holding
// secrets:read on its own tenant — the ordinary shape for an agent's
// JWT — gets metadata but no value back for a broker-only secret. The
// store-level half (Store.Get returning ErrBrokerOnly) is covered in
// internal/secrets; this test is what proves the RPC handler doesn't
// undo that by mapping the error to something that leaks the value.
func TestGetSecretRPC_BrokerRow_EmptyValueEvenWithSecretsRead(t *testing.T) {
	store := mustTestSecretsStore(t)
	s := &ContainerServer{secretsStore: store}
	const user = "broker-rpc-test-user"

	setCtx := kmsKeyTestCtx(user, "member", "secrets:write")
	if _, err := s.SetSecret(setCtx, &pb.SetSecretRequest{
		Username:     user,
		Name:         "GH_TOKEN",
		Value:        "ghp_secret",
		DeliveryMode: pb.SecretDelivery_SECRET_DELIVERY_BROKER_ONLY,
	}); err != nil {
		t.Fatalf("SetSecret(broker-only): %v", err)
	}

