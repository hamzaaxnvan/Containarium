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
