package sentinel

import (
	"net/http"
	"os"

	"github.com/footprintai/containarium/internal/sentinel/sshban"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// sshBanHandler discovers enabled local listeners and forwards to their private
// control sockets. Tests inject sockets without altering host provisioning.
func (m *Manager) sshBanHandler() http.Handler {
	return (&sshban.Proxy{ResolveSocket: m.sshBanSocket}).Handler()
