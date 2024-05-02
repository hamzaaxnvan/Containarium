package container

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

// TestValidateGPUStopsBeforeDeleting models Incus refusing to delete a running instance.
func TestValidateGPUStopsBeforeDeleting(t *testing.T) {
	for _, probeFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("probe fails=%v", probeFails), func(t *testing.T) {
			running, exists := false, false
			mock := &incustest.MockBackend{
				CreateContainerFunc: func(incus.ContainerConfig) error { exists = true; return nil },
				StartContainerFunc:  func(string) error { running = true; return nil },
				StopContainerFunc: func(_ string, force bool) error {
					if !force {
						t.Error("probe cleanup must force-stop its own instance")
					}
					running = false
					return nil
				},
				DeleteContainerFunc: func(string) error {
					if running {
						return errors.New("cannot delete running instance")
					}
					exists = false
					return nil
				},
				ExecWithOutputFunc: func(string, []string) (string, string, error) {
					if probeFails {
						return "", "", errors.New("probe failed")
					}
					return "test GPU, test driver", "", nil
				},
			}
			got := NewWithBackend(mock).ValidateGPU("")
			if exists || running {
				t.Fatal("GPU probe left its instance behind")
			}
			if !probeFails && got.Status != GPUStatusOK {
				t.Fatalf("successful probe: %+v", got)
			}
		})
	}
}
