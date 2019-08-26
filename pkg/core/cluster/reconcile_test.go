package cluster

import (
	"reflect"
	"testing"
)

func TestDecide(t *testing.T) {
	desired := Desired{
		Tenant:  "alice",
		Cluster: "demo",
		Groups: []DesiredGroup{
			{Name: "small", Min: 2, Max: 3},
			{Name: "large", Min: 0, Max: 1},
		},
	}
	cp := func(running bool) *ObservedVM {
		return &ObservedVM{Name: "alice-k8s-demo-cp", Running: running}
	}

	cases := []struct {
		name     string
		desired  Desired
		observed Observed
		want     []Action
	}{
		{
			"fresh cluster creates the control plane first, no workers yet",
			desired,
			Observed{},
			[]Action{{Kind: ActionCreateCP, Name: "alice-k8s-demo-cp"}},
		},
		{
