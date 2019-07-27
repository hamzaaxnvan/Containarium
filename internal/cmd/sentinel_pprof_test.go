//go:build !windows && !containarium_client

package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/sentinel"
	"github.com/spf13/cobra"
)

func TestBuildPprofURL(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		profile string
		seconds int
		debug   int
		gc      bool
		list    bool
		want    string
		wantErr string
	}{
		{
			name: "plain heap", base: "http://sentinel.example:8888", profile: "heap",
			want: "http://sentinel.example:8888/debug/pprof/heap",
		},
		{
			name: "trailing slash on base is not doubled", base: "http://sentinel.example:8888/", profile: "heap",
			want: "http://sentinel.example:8888/debug/pprof/heap",
		},
		{
			name: "heap with forced GC", base: "https://sentinel.example:8889", profile: "heap", gc: true,
			want: "https://sentinel.example:8889/debug/pprof/heap?gc=1",
		},
		{
