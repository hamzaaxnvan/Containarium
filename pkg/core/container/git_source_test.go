package container

import "strings"

import "testing"

func TestGitWorkspacePath(t *testing.T) {
	if got := gitWorkspacePath(""); got != "/workspace" {
		t.Errorf("empty → %q, want /workspace", got)
	}
	if got := gitWorkspacePath("  "); got != "/workspace" {
		t.Errorf("blank → %q, want /workspace", got)
	}
	if got := gitWorkspacePath("/srv/app"); got != "/srv/app" {
		t.Errorf("explicit → %q, want /srv/app", got)
	}
}

func TestBuildGitFetchScript_PublicRepoWithRef(t *testing.T) {
	s := buildGitFetchScript("https://github.com/org/repo", "abc123", "", "")

	wantContains := []string{
		"git init -q",
		"fetch --depth 1 -q 'https://github.com/org/repo' 'abc123'",
		"git checkout -q FETCH_HEAD",
		"mkdir -p '/workspace'",
	}
	for _, w := range wantContains {
		if !strings.Contains(s, w) {
			t.Errorf("script missing %q\n---\n%s", w, s)
		}
	}
	if strings.Contains(s, "http.extraHeader") {
		t.Errorf("public repo must not inject an auth header:\n%s", s)
	}
