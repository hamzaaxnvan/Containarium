package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/anonbox"
)

func TestAnonReminderWebhook_Send(t *testing.T) {
	var got anonReminderPayload
	var ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	exp := time.Date(2026, 10, 2, 16, 0, 0, 0, time.UTC)
	err := newAnonReminderWebhook(srv.URL).Send(context.Background(), anonbox.Reminder{
		Email: "alice@example.com", BoxName: "anon-1a2b3c4d-container", ClaimURL: "https://cloud.example.test/claim?token=v1.x", ExpiresAt: exp,
