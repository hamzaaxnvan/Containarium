package anonbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type recReminder struct {
	sent []Reminder
	err  error
}

func (r *recReminder) Send(_ context.Context, rem Reminder) error {
	r.sent = append(r.sent, rem)
	return r.err
}

// reminderHarness: claims + reminders enabled, one box ensured, the
// opt-in file's content controlled through the fake's exec output.
func reminderHarness(t *testing.T) (*harness, *recReminder, *recFunnel, string, time.Time) {
	t.Helper()
	rem := &recReminder{}
	f := &recFunnel{}
	h := newHarness(t, func(c *Config) { c.Reminder = rem; c.Funnel = f })
	start := h.now
	key, fp := testKey(t)
	res, err := h.m.Ensure(context.Background(), EnsureRequest{Fingerprint: fp, PublicKey: key})
	if err != nil {
		t.Fatal(err)
	}
	// provision pre-created the empty opt-in file, world-writable
	rf := fileNamed(t, h.boxes.files[res.BoxName], RemindFilePath)
	if rf.mode != "0666" || rf.content != "" {
		t.Fatalf("remind-me file = %+v, want empty 0666", rf)
	}
	banner := fileNamed(t, h.boxes.files[res.BoxName], BannerPath)
	if !strings.Contains(banner.content, "containarium remind-me") {
		t.Errorf("banner lacks the remind-me hint")
	}
	h.boxes.execs = nil
	f.events = nil
	return h, rem, f, res.BoxName, start
}

func TestReminder_OptInInsideLast30MinSentOnce(t *testing.T) {
	h, rem, f, box, start := reminderHarness(t)
