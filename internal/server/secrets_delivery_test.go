package server

import (
	"testing"

	"github.com/footprintai/containarium/internal/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestDeliveryToProto(t *testing.T) {
	tests := []struct {
		in   string
		want pb.SecretDelivery
	}{
		{secrets.DeliveryEnv, pb.SecretDelivery_SECRET_DELIVERY_ENV},
		{secrets.DeliveryFile, pb.SecretDelivery_SECRET_DELIVERY_FILE},
		{secrets.DeliveryCompose, pb.SecretDelivery_SECRET_DELIVERY_COMPOSE},
		{secrets.DeliveryBroker, pb.SecretDelivery_SECRET_DELIVERY_BROKER_ONLY},
		// "" is "unset" in storage, which the store normalizes to env — so
		// the enum must report ENV, not UNSPECIFIED, or the typed view would
		// disagree with what actually happens to the secret.
		{"", pb.SecretDelivery_SECRET_DELIVERY_ENV},
		{"bogus", pb.SecretDelivery_SECRET_DELIVERY_UNSPECIFIED},
	}
	for _, tc := range tests {
		if got := deliveryToProto(tc.in); got != tc.want {
			t.Errorf("deliveryToProto(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestDeliveryFromProto(t *testing.T) {
	tests := []struct {
		in   pb.SecretDelivery
		want string
	}{
		{pb.SecretDelivery_SECRET_DELIVERY_ENV, secrets.DeliveryEnv},
		{pb.SecretDelivery_SECRET_DELIVERY_FILE, secrets.DeliveryFile},
