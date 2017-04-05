package depsguard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeSumDB serves /lookup/<escaped path>@<escaped version> the way
// sum.golang.org does: a tree-id line, the two hash lines, then a signed note
// we do not verify. Anything not listed is a 404, as for a module the
// checksum database has never seen.
func fakeSumDB(t *testing.T, records map[string]SumRecord) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/lookup/")
		at := strings.LastIndex(key, "@")
		if at < 0 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		path, version := key[:at], key[at+1:]
		rec, ok := records[path+"@"+version]
		if !ok {
			http.Error(w, "not found: "+key, http.StatusNotFound)
			return
		}
		unescaped := unescapeForTest(path)
		v := unescapeForTest(version)
		_, _ = w.Write([]byte("1234\n" +
			unescaped + " " + v + " " + rec.Hash + "\n" +
			unescaped + " " + v + "/go.mod " + rec.GoModHash + "\n\ngo.sum database tree\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func unescapeForTest(s string) string {
	var b strings.Builder
	bang := false
	for _, r := range s {
		if r == '!' {
			bang = true
			continue
		}
		if bang {
			r = r - 'a' + 'A'
			bang = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

type fakeInfo struct {
	time   string
	origin string
}

func fakeProxy(t *testing.T, infos map[string]fakeInfo) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /<escaped path>/@v/<escaped version>.info
		p := strings.TrimPrefix(r.URL.Path, "/")
		i := strings.Index(p, "/@v/")
		if i < 0 || !strings.HasSuffix(p, ".info") {
			http.NotFound(w, r)
			return
		}
		key := p[:i] + "@" + strings.TrimSuffix(p[i+4:], ".info")
		info, ok := infos[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		body := `{"Version":"x","Time":"` + info.time + `"`
		if info.origin != "" {
			body += `,"Origin":{"VCS":"git","URL":"` + info.origin + `"}`
		}
		_, _ = w.Write([]byte(body + "}"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The hashes are the real sum.golang.org records for these versions, so the
// same fixtures also pass or fail the same way against the live service
// (scripts/ci/deps-guard.sh --self-test).
var realRecords = map[string]SumRecord{
	"gopkg.in/yaml.v3@v3.0.1": {
		Hash:      "h1:fxVm/GzAzEWqLHuvctI91KS9hhNmmWOoWu0XTYJS7CA=",
		GoModHash: "h1:K4uyk7z7BCEPqu6E+C64Yfv1cQ7kz7rIZviUmN+EgEM=",
	},
	"golang.org/x/text@v0.3.8": {
		Hash:      "h1:nAL+RVCQ9uMn3vJZbV+MRnydTJFPf8qqY42YiA6MrqY=",
		GoModHash: "h1:E6s5w1FMmriuDzIBO73fBruAKo1PCIq6d2Q6DHfQ8WQ=",
	},
}

var fixedNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func newChecker(t *testing.T) Checker {
	t.Helper()
	sum := fakeSumDB(t, realRecords)
	proxy := fakeProxy(t, map[string]fakeInfo{
		"gopkg.in/yaml.v3@v3.0.1": {time: "2022-05-27T08:35:30Z"},
	})
	return Checker{
		SumDB: HTTPSumDB{BaseURL: sum.URL, Client: sum.Client()},
		Proxy: HTTPProxy{BaseURL: proxy.URL, Client: proxy.Client()},
		Now:   func() time.Time { return fixedNow },
	}
}

func loadFixture(t *testing.T, dir string) (GoMod, GoMod, GoSum, string) {
	t.Helper()
	base, err := LoadGoMod(context.Background(), filepath.Join(dir, "base.go.mod"))
	if err != nil {
		t.Fatalf("load base: %v", err)
	}
	head, err := LoadGoMod(context.Background(), filepath.Join(dir, "head.go.mod"))
	if err != nil {
		t.Fatalf("load head: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "head.go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := ParseGoSum(b)
	if err != nil {
		t.Fatal(err)
	}
	expect, err := os.ReadFile(filepath.Join(dir, "expect"))
	if err != nil {
		t.Fatal(err)
	}
	return base, head, sum, strings.TrimSpace(string(expect))
}

// One fixture per failure, plus the passing one. Every fixture directory is
// exercised, and the set must contain at least one pass and one fail, so a
// deleted fixture or a guard that never fails cannot leave this green.
func TestFixtures(t *testing.T) {
	dirs, err := filepath.Glob("testdata/*")
	if err != nil {
