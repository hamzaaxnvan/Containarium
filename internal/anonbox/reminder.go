package anonbox

import (
	"context"
	"net/mail"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/box"
)

// Opt-in expiry reminder (#2206, decision on #2204: contact capture is
// opt-in only). A process inside the box has no daemon credential, so
// `containarium remind-me <email>` writes RemindFilePath — a file the
// daemon pre-creates world-writable at provision time — and the warner
// reads it on its tick inside the last ReminderBefore of the TTL. A
// non-empty, valid address is copied to LabelRemindEmail, a FunnelReminderOptIn
// is recorded, the reminder is handed to Config.Reminder exactly once, and
// the address label is cleared again: nothing about the user outlives the
// one delivery.
const (
	RemindFilePath   = "/etc/containarium/remind-me"
	LabelRemindEmail = "anon.remind_email" // set only between pickup and delivery
