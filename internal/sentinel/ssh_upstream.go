package sentinel

import (
	"context"
	"fmt"
	"github.com/footprintai/containarium/internal/sentinel/sshcontrol"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

// BackendForEndpoint resolves enforcement identity from authoritative key routes.
func (ks *KeyStore) BackendForEndpoint(endpoint string) (string, error) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	id := ""
	for _, b := range ks.backends {
		if net.JoinHostPort(b.backendIP, strconv.Itoa(routeTargetPort(b.backendIP, b.sshPort))) == endpoint {
			if id != "" && id != b.backendID {
				return "", fmt.Errorf("ambiguous SSH endpoint")
			}
			id = b.backendID
		}
	}
	if id == "" {
		return "", fmt.Errorf("unknown SSH route %s", endpoint)
	}
	return id, nil
