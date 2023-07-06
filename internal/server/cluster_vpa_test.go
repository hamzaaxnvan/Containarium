//go:build !containarium_client

package server

// VPA deployment through the reconciler (#1416): the pinned bundle and
// a per-cluster webhook secret land on the CP via the k3s auto-apply
// dir, exactly once.

import (
