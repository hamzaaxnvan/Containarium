package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

// srv0Of decodes the fake Caddy's full config and returns the srv0 map.
func srv0Of(t *testing.T, url string) map[string]interface{} {
	t.Helper()
	resp, err := http.Get(url + "/config/")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	defer resp.Body.Close()
	var cfg map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&cfg)
	return cfg["apps"].(map[string]interface{})["http"].(map[string]interface{})["servers"].(map[string]interface{})["srv0"].(map[string]interface{})
}

func strSlice(v interface{}) []string {
	raw, _ := v.([]interface{})
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		out = append(out, x.(string))
	}
	return out
}

func eqSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func clientIPInitialConfig() map[string]interface{} {
	return map[string]interface{}{
		"apps": map[string]interface{}{
			"http": map[string]interface{}{
				"servers": map[string]interface{}{
					"srv0": map[string]interface{}{
						"listen": []interface{}{":80", ":443"},
						"routes": []interface{}{
							map[string]interface{}{
								"@id":    "app.example.com",
								"match":  []interface{}{map[string]interface{}{"host": []interface{}{"app.example.com"}}},
								"handle": []interface{}{map[string]interface{}{"handler": "reverse_proxy", "upstreams": []interface{}{map[string]interface{}{"dial": "10.0.3.5:8080"}}}},
							},
						},
					},
				},
			},
		},
	}
}

// The core #1829 contract: after PROXY protocol is enabled for the sentinel,
// ConfigureClientIP must (a) set client_ip_headers, (b) widen trusted_proxies
// to the union of the sentinel set and the CDN ranges, and (c) leave the
// proxy_protocol allow list as the sentinel set only — a CDN never sends PROXY
// headers, and widening that allow list would let a CDN edge spoof a source.
func TestProxyManager_ConfigureClientIP_SetsHeadersUnionsRangesKeepsAllow(t *testing.T) {
	srv := newFakeCaddy(clientIPInitialConfig())
	defer srv.Close()
