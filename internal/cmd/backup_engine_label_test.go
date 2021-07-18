package cmd

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `backup get` printed "postgres" before the engine became an enum (#1157).
// Printing the raw enum would show BACKUP_ENGINE_POSTGRES to a human for no
// reason — the type change is for the contract, not the console.
func TestEngineLabel(t *testing.T) {
