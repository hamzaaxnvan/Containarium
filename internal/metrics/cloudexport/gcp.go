package cloudexport

import (
	"context"
	"crypto/tls"
	"fmt"

	gcpdetector "go.opentelemetry.io/contrib/detectors/gcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"golang.org/x/oauth2/google"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
)

// monitoringWriteScope is the OAuth2 scope required to write custom
// metrics to Google Cloud Monitoring (CreateTimeSeries).
const monitoringWriteScope = "https://www.googleapis.com/auth/monitoring.write"

// gcpOTLPEndpoint is Google Cloud's Telemetry API OTLP ingestion endpoint
// (see https://cloud.google.com/stackdriver/docs/otlp-metrics/overview).
// It accepts standard OTLP-over-gRPC and writes each metric into Google's
// Prometheus namespace on a prometheus_target resource: a gauge becomes
// prometheus.googleapis.com/<name>/gauge and a monotonic counter
// prometheus.googleapis.com/<name>/counter. The deprecated
// opentelemetry-operations-go exporter used workload.googleapis.com/<name> on
// gce_instance, so series names and the monitored resource CHANGED with the
// migration (v0.100.0): alert filters and dashboards written for the old type
// select nothing. See "Metric names and resource type" in
// docs/METRICS-EXPORT-DEADMAN-ALERT-RUNBOOK.md.
const gcpOTLPEndpoint = "telemetry.googleapis.com:443"

// projectHeader is the header Google Cloud's OTLP ingestion reads to
// attribute incoming series to a project (the OTLP equivalent of the
// deprecated exporter's WithProjectID option), mirroring the
// "x-goog-user-project" convention used across GCP client libraries.
const projectHeader = "x-goog-user-project"

// gcpCredentialsLookup resolves Application Default Credentials scoped
// for Cloud Monitoring writes. It is a package-level var — rather than a
// direct call to google.FindDefaultCredentials — so tests can inject a
// fake lookup (missing ADC, or a credential whose token source fails)
// without touching the network or the real ADC search path
// (GOOGLE_APPLICATION_CREDENTIALS, gcloud's well-known file, or the GCE
// metadata server).
var gcpCredentialsLookup = func(ctx context.Context) (*google.Credentials, error) {
	return google.FindDefaultCredentials(ctx, monitoringWriteScope)
}

// gcpSink implements Sink for Google Cloud Monitoring.
type gcpSink struct{}

// NewGCPSink returns the GCP Sink implementation.
func NewGCPSink() Sink { return &gcpSink{} }

// NewExporter builds the OTel SDK metric exporter that pushes to Google
// Cloud Monitoring over standard OTLP-over-gRPC, per upstream's
// MIGRATION.md for the now-deprecated opentelemetry-operations-go
// exporter (#1979). Authentication is ADC, unchanged from before the
// migration: gcpCredentialsLookup resolves the same Application Default
// Credentials that Probe validates at enable time, wrapped as gRPC
// per-RPC credentials — no key file is ever read here, and no separate
// monitoring API client is built. This is the sole place in the daemon
// that dials the GCP OTLP endpoint — everything else sees the Sink
// interface.
func (g *gcpSink) NewExporter(ctx context.Context, cfg SinkConfig) (sdkmetric.Exporter, error) {
	var opts []otlpmetricgrpc.Option

	if cfg.GRPCConn != nil {
		// Test seam: dial straight into a fake in-process OTLP collector,
		// bypassing TLS and ADC entirely.
		opts = append(opts, otlpmetricgrpc.WithGRPCConn(cfg.GRPCConn))
	} else {
		creds, err := gcpCredentialsLookup(ctx)
		if err != nil {
			return nil, fmt.Errorf("cloudexport: resolve ADC for GCP OTLP metric export: %w", err)
		}
		opts = append(opts,
			otlpmetricgrpc.WithEndpoint(gcpOTLPEndpoint),
			otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{})),
			otlpmetricgrpc.WithDialOption(grpc.WithPerRPCCredentials(oauth.TokenSource{TokenSource: creds.TokenSource})),
		)
	}

	// Empty ProjectID leaves project attribution to whatever quota
	// project ADC itself resolves (the GCE metadata server's project,
	// the common on-VM case) — same default behavior as the deprecated
	// exporter's empty WithProjectID.
	if cfg.ProjectID != "" {
		opts = append(opts, otlpmetricgrpc.WithHeaders(map[string]string{projectHeader: cfg.ProjectID}))
	}
