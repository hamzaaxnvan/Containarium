package mcp

import "testing"

func namesOf(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func TestFilterToolsByAllowlist_ExactNames(t *testing.T) {
	tools := []Tool{{Name: "create_container"}, {Name: "list_containers"}, {Name: "delete_container"}}
	got, err := filterToolsByAllowlist(tools, "create_container,delete_container")
	if err != nil {
		t.Fatalf("filterToolsByAllowlist: %v", err)
	}
	want := []string{"create_container", "delete_container"}
	if got := namesOf(got); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v (registration order preserved)", got, want)
	}
}

func TestFilterToolsByAllowlist_PrefixGlob(t *testing.T) {
	tools := []Tool{
		{Name: "tracker_get_issue"}, {Name: "tracker_claim_issue"},
		{Name: "create_container"}, {Name: "list_containers"},
	}
	got, err := filterToolsByAllowlist(tools, "tracker_*")
