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
			"stopped control plane is started, not replaced",
			desired,
			Observed{CP: cp(false)},
			[]Action{{Kind: ActionStartVM, Name: "alice-k8s-demo-cp"}},
		},
		{
			"running control plane converges each group to its min",
			desired,
			Observed{CP: cp(true)},
			[]Action{
				{Kind: ActionCreateWorker, Group: "small", Name: "alice-k8s-demo-small-1"},
				{Kind: ActionCreateWorker, Group: "small", Name: "alice-k8s-demo-small-2"},
			},
		},
		{
			"lost worker is replaced under an unused index",
			desired,
			Observed{CP: cp(true), Workers: map[string][]ObservedVM{
				"small": {{Name: "alice-k8s-demo-small-2", Running: true}},
			}},
			[]Action{
				{Kind: ActionCreateWorker, Group: "small", Name: "alice-k8s-demo-small-1"},
			},
		},
		{
			"stopped worker is started, never started AND replaced in one pass",
			desired,
			Observed{CP: cp(true), Workers: map[string][]ObservedVM{
				"small": {
					{Name: "alice-k8s-demo-small-1", Running: false},
					{Name: "alice-k8s-demo-small-2", Running: true},
				},
			}},
