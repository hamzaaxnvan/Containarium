package server

import "github.com/footprintai/containarium/internal/threatdetect"

// Exec stamps (#2765 follow-up). The exec watcher stamps every decoded exec
// with the boot clock instead of reading /proc/<pid>/stat for it: a stamp is
// one clock read, a stat read is open/fstat/read/close on procfs plus the
// kernel formatting ~50 fields, and the consumer drains the exec ring on
// every exec of every watched box. The kill reads the stat once, for the one
// process it is about to signal, and compares the two.
//
// Why the compare is sound. /proc/<pid>/stat field 22 is the process's fork
// time on CLOCK_BOOTTIME (task->start_boottime), floored to USER_HZ ticks
// (nsec_to_clock_t). The stamp is CLOCK_BOOTTIME read after the ring record
// was consumed, floored the same way. The matched process forked before it
// exec'd, and it exec'd before its record reached the ring, so its start
// time is <= the stamp. A process that holds the pid at kill time and
// started after the stamp is therefore not the matched process: refused as
// pid reused. Both values are read through the daemon's own time namespace
// (procfs adds the reader's boottime offset), so they stay comparable.
//
// The residual window is exec -> stamp: at most one ring drain
