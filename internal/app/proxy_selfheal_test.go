package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rwFakeCaddy is a fuller stand-in for the Caddy admin API than newFakeCaddy:
// it also honours PUT on a /config/<path> (creating intermediate maps), which
// is what EnsureServerConfig / createHTTPApp / createTLSApp use to rebuild the
// base config. GET walks the tree; POST /load replaces it; PUT sets a subtree.
type rwFakeCaddy struct {
	config map[string]interface{}
	loads  int
	puts   int
}

func newRWFakeCaddy(initial map[string]interface{}) (*httptest.Server, *rwFakeCaddy) {
	if initial == nil {
		initial = map[string]interface{}{}
	}
	fc := &rwFakeCaddy{config: initial}
	mux := http.NewServeMux()
	mux.HandleFunc("/config/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/config/")
		path = strings.TrimSuffix(path, "/")

		// PATCH is treated like PUT: ProvisionTLS writes the policy array back
		// with PATCH, and for a whole-node replacement the two are equivalent.
		if r.Method == http.MethodPut || r.Method == http.MethodPatch {
			body, _ := io.ReadAll(r.Body)
			var val interface{}
			if err := json.Unmarshal(body, &val); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			segs := strings.Split(path, "/")
			node := fc.config
			for _, p := range segs[:len(segs)-1] {
				next, ok := node[p].(map[string]interface{})
				if !ok {
					next = map[string]interface{}{}
					node[p] = next
				}
				node = next
			}
			node[segs[len(segs)-1]] = val
			fc.puts++
			w.WriteHeader(http.StatusOK)
			return
		}

		// GET
		if path == "" {
			_ = json.NewEncoder(w).Encode(fc.config)
			return
		}
		var node interface{} = fc.config
		for _, p := range strings.Split(path, "/") {
			m, ok := node.(map[string]interface{})
			if !ok {
				http.Error(w, "null", http.StatusNotFound)
				return
			}
			node = m[p]
		}
		_ = json.NewEncoder(w).Encode(node)
	})
	// DELETE /id/<route-id> — real Caddy's @id addressing, used by
	// ProxyManager.RemoveRoute. Removes the matching route from the http
	// server's route list. Additive: no pre-existing test uses this path.
	mux.HandleFunc("/id/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/id/")
		apps, _ := fc.config["apps"].(map[string]interface{})
		httpApp, _ := apps["http"].(map[string]interface{})
		servers, _ := httpApp["servers"].(map[string]interface{})
		srv, _ := servers[DefaultCaddyServerName].(map[string]interface{})
		routes, _ := srv["routes"].([]interface{})

		kept := make([]interface{}, 0, len(routes))
		found := false
		for _, rt := range routes {
			m, _ := rt.(map[string]interface{})
			if m != nil {
				if got, _ := m["@id"].(string); got == id {
					found = true
					continue
				}
			}
			kept = append(kept, rt)
		}
		if !found {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		srv["routes"] = kept
		fc.puts++
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var newCfg map[string]interface{}
		if err := json.Unmarshal(body, &newCfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fc.config = newCfg
		fc.loads++
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewServer(mux), fc
}

