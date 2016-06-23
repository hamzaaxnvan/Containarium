package reqrate

import "testing"

func TestParseHost(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantHost string
		wantOK   bool
	}{
		{
			name:     "typical caddy access record",
			line:     `{"level":"info","ts":1718000000.1,"logger":"http.log.access.log0","msg":"handled request","request":{"remote_ip":"203.0.113.7","method":"GET","host":"alice.example.com","uri":"/"},"status":200}`,
			wantHost: "alice.example.com",
			wantOK:   true,
		},
		{
			name:     "host carries a port",
