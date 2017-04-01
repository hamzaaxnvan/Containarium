//go:build windows

package cloud

// diskGB / gpuInfo are no-op stubs on Windows: the daemon doesn't run there,
// but internal/cloud is in the cross-platform base binary's import graph, so
// these must compile. They report zeros (the cloud treats zero as "not
// reported").
