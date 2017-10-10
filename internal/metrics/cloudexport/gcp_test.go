package cloudexport

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// fakeTokenSource lets tests simulate a credential that resolves but
// can't actually mint a token (revoked, wrong scope, expired refresh
// token, ...) without any network call.
type fakeTokenSource struct {
	token *oauth2.Token
	err   error
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.token, nil
}

// TestProbe_ADC is the table from the design doc's test strategy:
// (no ADC / wrong-scope token / ok) -> (actionable error / actionable
// error / nil). No network or filesystem ADC search is exercised —
// gcpCredentialsLookup is swapped for the duration of each case.
func TestProbe_ADC(t *testing.T) {
	tests := []struct {
		name    string
		lookup  func(ctx context.Context) (*google.Credentials, error)
		wantErr bool
		wantSub string // substring the error must contain, if wantErr
	}{
		{
			name: "no ADC configured",
			lookup: func(ctx context.Context) (*google.Credentials, error) {
				return nil, errors.New("could not find default credentials")
			},
			wantErr: true,
			wantSub: "roles/monitoring.metricWriter",
		},
		{
			name: "credentials resolve but token source is nil",
			lookup: func(ctx context.Context) (*google.Credentials, error) {
				return &google.Credentials{}, nil
			},
			wantErr: true,
			wantSub: "roles/monitoring.metricWriter",
		},
		{
			name: "credentials resolve but token mint fails (revoked / wrong scope)",
			lookup: func(ctx context.Context) (*google.Credentials, error) {
				return &google.Credentials{
					TokenSource: &fakeTokenSource{err: errors.New("invalid_grant: token has been revoked")},
				}, nil
			},
			wantErr: true,
			wantSub: "roles/monitoring.metricWriter",
		},
		{
			name: "ok",
			lookup: func(ctx context.Context) (*google.Credentials, error) {
				return &google.Credentials{
					TokenSource: &fakeTokenSource{token: &oauth2.Token{
						AccessToken: "fake-token",
						Expiry:      time.Now().Add(time.Hour),
					}},
				}, nil
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			orig := gcpCredentialsLookup
			gcpCredentialsLookup = tc.lookup
			defer func() { gcpCredentialsLookup = orig }()

			sink := NewGCPSink()
			err := sink.Probe(context.Background())

			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if tc.wantErr && tc.wantSub != "" && !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain expected hint %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// fakeOTLPMetricsServer is an in-process OTLP metrics collector: it
// records the ExportMetricsServiceRequest batches the real
// otlpmetricgrpc exporter code path sends, so the exporter can be
// exercised end-to-end without touching Google Cloud's OTLP ingestion
// endpoint (#1979 migration off the deprecated GAPIC-based exporter).
type fakeOTLPMetricsServer struct {
	colmetricpb.UnimplementedMetricsServiceServer
	mu       sync.Mutex
	requests []*colmetricpb.ExportMetricsServiceRequest
}

func (f *fakeOTLPMetricsServer) Export(ctx context.Context, req *colmetricpb.ExportMetricsServiceRequest) (*colmetricpb.ExportMetricsServiceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	return &colmetricpb.ExportMetricsServiceResponse{}, nil
}

func (f *fakeOTLPMetricsServer) received() []*colmetricpb.ExportMetricsServiceRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*colmetricpb.ExportMetricsServiceRequest(nil), f.requests...)
}

// metricNames flattens every Metric.Name across every ResourceMetrics/
// ScopeMetrics in every received batch, deduplicated and sorted — the
// exact set of OTLP metric names the exporter sent, independent of how
// many ticks/batches they arrived in.
func (f *fakeOTLPMetricsServer) metricNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, req := range f.received() {
		for _, rm := range req.GetResourceMetrics() {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					if name := m.GetName(); !seen[name] {
						seen[name] = true
						names = append(names, name)
					}
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

// startFakeOTLPCollector spins up the fake OTLP metrics collector on a
// loopback listener and returns it plus an insecure gRPC connection
// pointed at it, suitable for SinkConfig.GRPCConn.
func startFakeOTLPCollector(t *testing.T) (*fakeOTLPMetricsServer, *grpc.ClientConn) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fake := &fakeOTLPMetricsServer{}
	srv := grpc.NewServer()
	colmetricpb.RegisterMetricsServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial fake OTLP collector: %v", err)
	}
