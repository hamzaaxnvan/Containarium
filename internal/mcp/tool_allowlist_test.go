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
	if err != nil {
		t.Fatalf("filterToolsByAllowlist: %v", err)
	}
	names := namesOf(got)
	if len(names) != 2 || names[0] != "tracker_get_issue" || names[1] != "tracker_claim_issue" {
		t.Fatalf("got %v, want exactly the two tracker_* tools, in order", names)
	}
}

func TestFilterToolsByAllowlist_MixedExactAndGlob(t *testing.T) {
	tools := []Tool{
		{Name: "tracker_get_issue"}, {Name: "tracker_claim_issue"}, {Name: "create_container"},
	}
	got, err := filterToolsByAllowlist(tools, "tracker_*, create_container")
	if err != nil {
		t.Fatalf("filterToolsByAllowlist: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d tools, want 3", len(got))
	}
}

func TestFilterToolsByAllowlist_OverlappingEntriesDoNotDuplicate(t *testing.T) {
	tools := []Tool{{Name: "tracker_get_issue"}, {Name: "tracker_claim_issue"}}
	// "tracker_*" and the exact name both match tracker_get_issue.
	got, err := filterToolsByAllowlist(tools, "tracker_*,tracker_get_issue")
	if err != nil {
		t.Fatalf("filterToolsByAllowlist: %v", err)
	}
	if len(got) != 2 {
