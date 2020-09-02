package sentinel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoopbackPortsFor(t *testing.T) {
	tests := []struct {
		name       string
		ports      []int
		httpsPort  int
		publicPort int
		want       []int
	}{
		{
			name:      "non-primary backend still excludes the sentinel's own https port",
			ports:     []int{22, 8080, 443},
