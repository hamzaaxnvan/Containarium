//go:build !windows

package agentbox

import (
	"testing"
	"time"
)

// The exported decode helpers (#2123) are the same rules listRunRecords and
// readRunRecord apply, so the daemon's box-run reader cannot drift from what
// process_list reports.
func TestDecodeRunRecord(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		wantErr bool
		want    string
	}{
		{name: "current version", data: `{"version":2,"name":"task","pid":7}`, want: "task"},
		{name: "v1 still readable", data: `{"version":1,"name":"old"}`, want: "old"},
		{name: "future version rejected", data: `{"version":99,"name":"x"}`, wantErr: true},
		{name: "zero version rejected", data: `{"name":"x"}`, wantErr: true},
		{name: "malformed", data: `{`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := DecodeRunRecord([]byte(tc.data))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && r.Name != tc.want {
				t.Errorf("name = %q, want %q", r.Name, tc.want)
			}
		})
	}
}

func TestFoldExitSidecar(t *testing.T) {
	seven := 7
	cases := []struct {
		name     string
		record   RunRecord
		sidecar  string
		wantCode *int
		wantAt   time.Time
	}{
		{name: "no sidecar leaves it open", record: RunRecord{Name: "a"}},
		{name: "sidecar supplies code and time", record: RunRecord{Name: "a"}, sidecar: "3 1790000000", wantCode: intPtr(3), wantAt: time.Unix(1790000000, 0).UTC()},
		{name: "corrupt sidecar ignored", record: RunRecord{Name: "a"}, sidecar: "x"},
