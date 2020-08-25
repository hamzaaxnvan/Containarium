package backupkey

import (
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/footprintai/containarium/pkg/core/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const testKEKID = "gcp:projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>/cryptoKeyVersions/1"

func TestUnwrapIdentity(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	managed := &pb.BackupRecord{Id: "alice-app-x", WrappedKey: []byte("wrapped"), KekId: testKEKID}

	t.Run("unwraps the record's key under the loaded client", func(t *testing.T) {
		kms := &fakeKMS{Plaintext: []byte(id.String())}
		var loadedFor string
		got, err := UnwrapIdentity(context.Background(), func(kekID string) (secrets.KMSClient, error) {
			loadedFor = kekID
			return kms, nil
		}, managed)
		if err != nil {
			t.Fatalf("UnwrapIdentity: %v", err)
		}
		if string(got) != id.String() {
			t.Error("returned identity differs from the KMS plaintext")
		}
		if loadedFor != testKEKID || kms.GotKEKID != testKEKID || string(kms.GotWrapped) != "wrapped" {
			t.Errorf("unwrap used kek %q / loader %q / wrapped %q", kms.GotKEKID, loadedFor, kms.GotWrapped)
		}
	})

	t.Run("a record without a managed key is refused before any KMS call", func(t *testing.T) {
		called := false
		_, err := UnwrapIdentity(context.Background(), func(string) (secrets.KMSClient, error) {
			called = true
			return &fakeKMS{}, nil
		}, &pb.BackupRecord{Id: "alice-app-x", Encrypted: true, AgeRecipient: "age1example"})
		if err == nil || !strings.Contains(err.Error(), "no managed key") {
			t.Fatalf("err = %v, want a 'no managed key' refusal", err)
		}
		if called {
			t.Error("KMS loaded for a record that has nothing to unwrap")
		}
	})

	t.Run("an unwrap error is returned with its cause", func(t *testing.T) {
		denied := errors.New("PermissionDenied: caller lacks cloudkms.cryptoKeyVersions.useToDecrypt")
		_, err := UnwrapIdentity(context.Background(), func(string) (secrets.KMSClient, error) {
			return &fakeKMS{Err: denied}, nil
		}, managed)
		if !errors.Is(err, denied) {
			t.Fatalf("err = %v, want it to wrap the KMS error", err)
		}
	})

	t.Run("plaintext that is not an age identity is zeroed and refused", func(t *testing.T) {
		kms := &fakeKMS{Plaintext: []byte("not-an-identity")}
		_, err := UnwrapIdentity(context.Background(), func(string) (secrets.KMSClient, error) { return kms, nil }, managed)
		if err == nil || !strings.Contains(err.Error(), "age identity") {
			t.Fatalf("err = %v, want a refusal naming the age identity", err)
		}
		for _, b := range kms.Plaintext {
			if b != 0 {
				t.Fatal("rejected plaintext was not zeroed")
			}
		}
	})
}
