package hostcheck

// PlatformCPUWeight is the systemd CPUWeight= the platform's own daemons
// run at (#2284). systemd's default — and therefore every tenant instance's
// effective weight — is 100; 10× that keeps `incusd` and the containarium
// daemon scheduling when ~70 tenants saturate the host, without starving
// tenants outright the way a quota or an absolute priority would.
//
// No build constraint on this file: the Windows cmd build references the
// constant from the unit-template tests even though the check itself is
// Linux-only.
const PlatformCPUWeight = 1000

