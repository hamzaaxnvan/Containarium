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

// TestValidateGPUReportsCleanupFailure prevents a failed teardown from looking successful.
func TestValidateGPUReportsCleanupFailure(t *testing.T) {
	for _, failure := range []string{"stop", "delete"} {
		t.Run(failure, func(t *testing.T) {
			deleteAttempted := false
			mock := &incustest.MockBackend{
				StopContainerFunc: func(string, bool) error {
					if failure == "stop" {
						return errors.New("stop failed")
					}
					return nil
				},
				DeleteContainerFunc: func(string) error {
					deleteAttempted = true
					if failure == "delete" {
						return errors.New("delete failed")
					}
					return nil
				},
				ExecWithOutputFunc: func(string, []string) (string, string, error) { return "test GPU, test driver", "", nil },
			}
			got := NewWithBackend(mock).ValidateGPU("")
			if !deleteAttempted || got.Status != GPUStatusUnavailable || !strings.Contains(got.Detail, failure+" failed") {
				t.Fatalf("cleanup must attempt deletion and report failure: %+v", got)
			}
		})
	}
}

func init() {
	// Keep the readiness loop instant in tests.
	gpuValidationReadyTries = 1
	gpuValidationReadyDelay = 0
}

func TestParseNvidiaSmiCSV(t *testing.T) {
	cases := []struct{ in, model, driver string }{
		{"NVIDIA GeForce RTX 3090, 570.211.01", "NVIDIA GeForce RTX 3090", "570.211.01"},
		{"NVIDIA GeForce RTX 3090, 570.211.01\nNVIDIA A100, 535.0", "NVIDIA GeForce RTX 3090", "570.211.01"},
		{"  NVIDIA L4 ,  550.x  ", "NVIDIA L4", "550.x"},
		{"", "", ""},
		{"weird-no-comma", "weird-no-comma", ""},
	}
	for _, c := range cases {
		m, d := parseNvidiaSmiCSV(c.in)
		if m != c.model || d != c.driver {
			t.Errorf("parseNvidiaSmiCSV(%q) = (%q, %q), want (%q, %q)", c.in, m, d, c.model, c.driver)
		}
	}
}

func TestValidateGPU_OK(t *testing.T) {
	var created incus.ContainerConfig
	var nvruntime string
	started, deleted := false, false
	mock := &incustest.MockBackend{
		CreateContainerFunc: func(cfg incus.ContainerConfig) error { created = cfg; return nil },
		SetConfigFunc: func(_, k, v string) error {
			if k == "nvidia.runtime" {
				nvruntime = v
			}
			return nil
		},
		StartContainerFunc:  func(string) error { started = true; return nil },
		DeleteContainerFunc: func(string) error { deleted = true; return nil },
		ExecWithOutputFunc: func(_ string, _ []string) (string, string, error) {
			return "NVIDIA GeForce RTX 3090, 570.211.01\n", "", nil
		},
	}
	m := NewWithBackend(mock)

	got := m.ValidateGPU("0000:01:00.0")

	if got.Status != GPUStatusOK {
		t.Fatalf("status = %q (detail %q), want ok", got.Status, got.Detail)
	}
	if got.Model != "NVIDIA GeForce RTX 3090" || got.DriverVersion != "570.211.01" {
		t.Errorf("model/driver = %q / %q", got.Model, got.DriverVersion)
	}
	if len(created.GPUs) != 1 || created.GPUs[0].PCI != "0000:01:00.0" {
		t.Errorf("expected throwaway created with one GPU pci=0000:01:00.0, got %+v", created.GPUs)
	}
	if nvruntime != "true" {
		t.Errorf("expected nvidia.runtime=true set, got %q", nvruntime)
	}
	if !started {
		t.Error("expected the throwaway to be started")
	}
	if !deleted {
		t.Error("expected the throwaway to be deleted (teardown)")
	}
}

func TestValidateGPU_AllGPUsWhenNoPCI(t *testing.T) {
	var created incus.ContainerConfig
	mock := &incustest.MockBackend{
		CreateContainerFunc: func(cfg incus.ContainerConfig) error { created = cfg; return nil },
		ExecWithOutputFunc: func(_ string, _ []string) (string, string, error) {
			return "NVIDIA L4, 550.0\n", "", nil
		},
	}
