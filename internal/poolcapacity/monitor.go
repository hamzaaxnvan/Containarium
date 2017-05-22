// Package poolcapacity watches Incus storage-pool fill (#2823).
//
// A nearly full pool stops every container on the host from writing, core
// Postgres included, and the host-wide disk series (a sum over pools) can hide
// one full pool. The monitor reads each pool on a bounded tick, classifies it
// against fixed thresholds, logs on every level change (so a pool already past
// warn is logged at daemon start) and hands each sample to a metrics hook.
package poolcapacity

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Thresholds on used/total as Incus reports it. Fixed by decision on #2823;
// operators override them with custom alert rules. Incus' ZFS numbers exclude
// slop space (read from the driver, not verified on a live pool), so 95% here
// is roughly 92% of zpool capacity.
const (
	WarnPercent = 85
	CritPercent = 95
)

// Level is a pool's fill classification. It lives in incus so the client
// side (ServerInfo) can carry it without importing this package.
type Level = incus.StoragePoolLevel

const (
	LevelUnknown  = incus.StoragePoolLevelUnknown // total not reported
	LevelOK       = incus.StoragePoolLevelOK
	LevelWarning  = incus.StoragePoolLevelWarning
	LevelCritical = incus.StoragePoolLevelCritical
)

// Classify maps used/total onto a Level.
func Classify(used, total int64) Level {
	if total <= 0 {
		return LevelUnknown
	}
	switch pct := used * 100 / total; {
	case pct >= CritPercent:
		return LevelCritical
	case pct >= WarnPercent:
