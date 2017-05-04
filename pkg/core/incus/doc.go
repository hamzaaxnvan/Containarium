// Package incus wraps the Incus/LXC API used to drive container lifecycle,
// networking, and storage on a Containarium host. It exposes a concrete
// production *Client and a Backend interface that consumers depend on for
// mocking (see pkg/core/incus/incustest.MockBackend).
