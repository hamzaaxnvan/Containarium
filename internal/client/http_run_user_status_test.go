package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHTTPGetBoxRunUserStatus(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"runUser":"ctn-run","toolAccessRecorded":true,"runUserExists":true,"state":"CODE_CREDENTIAL_SOURCE_EXPIRED"}`))
	}))
	defer srv.Close()
	c, err := NewHTTPClient(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetBoxRunUserStatus("alice")
