package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/app/buildpack"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	v1 "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Manager orchestrates app deployment workflow
type Manager struct {
	store       AppStore
	builder     *Builder
	proxy       *ProxyManager
	incusClient incus.Backend
	baseDomain  string
}

// ManagerConfig holds configuration for the manager
type ManagerConfig struct {
	// BaseDomain is the domain for app subdomains (e.g., "containarium.dev")
	BaseDomain string

	// CaddyAdminURL is the URL for Caddy's admin API
	CaddyAdminURL string
}

// NewManager creates a new app manager
func NewManager(store AppStore, incusClient incus.Backend, config ManagerConfig) *Manager {
	detector := buildpack.NewDetector()
	builder := NewBuilder(incusClient, detector)
	proxy := NewProxyManager(config.CaddyAdminURL, config.BaseDomain).WithDNSChallenge(DNSChallengeFromEnv())
	warnIfACMEEmailUnset()

	return &Manager{
		store:       store,
		builder:     builder,
		proxy:       proxy,
		incusClient: incusClient,
		baseDomain:  config.BaseDomain,
	}
}

// warnIfACMEEmailUnset logs at startup when no ACME contact address is
// configured.
//
// The effect of leaving it unset is otherwise silent: the emitted policy just
// has one issuer instead of two, and no CA has an address to send expiry
// warnings to. Both only become visible at the moment they matter — when the
// primary issuer is rate-limited, or when a renewal has been stalled long
// enough to expire (#1616).
func warnIfACMEEmailUnset() {
	if ACMEEmailFromEnv() != "" {
		return
	}
	log.Printf("WARNING: CONTAINARIUM_ACME_EMAIL is unset — TLS policies will carry a single " +
		"Let's Encrypt issuer with no fallback (ZeroSSL refuses ACME registration without a " +
		"contact address), and neither CA can send certificate-expiry warnings. Set it to an " +
		"address the operators read.")
}

// DeployApp deploys a new application or updates an existing one
func (m *Manager) DeployApp(ctx context.Context, req *v1.DeployAppRequest) (*v1.App, *v1.DetectedLanguage, error) {
	// Validate app name
	if err := container.ValidateUserContainerName(req.AppName); err != nil {
		return nil, nil, fmt.Errorf("invalid app name: %w", err)
	}

	// Get user's container info
	containerName := req.Username + "-container" // User container follows {username}-container naming
	containerInfo, err := m.incusClient.GetContainer(containerName)
	if err != nil {
		return nil, nil, fmt.Errorf("user container not found: %w", err)
	}

	if containerInfo.State != "Running" {
		return nil, nil, fmt.Errorf("user container is not running")
	}

	// Generate subdomain if not provided
	subdomain := req.Subdomain
	if subdomain == "" {
		subdomain = fmt.Sprintf("%s-%s", req.Username, req.AppName)
	}

	// Default port
	port := req.Port
	if port == 0 {
		port = 3000
	}

	// Check if app already exists
	existingApp, err := m.store.GetByName(ctx, req.Username, req.AppName)
	var app *v1.App

	if err == nil && existingApp != nil {
		// Update existing app
		app = existingApp
		app.Port = port
		app.EnvVars = req.EnvVars
		app.State = v1.AppState_APP_STATE_UPLOADING
		app.UpdatedAt = timestamppb.Now()
		app.ErrorMessage = ""
	} else {
		// Create new app
		app = &v1.App{
			Id:            uuid.New().String(),
			Name:          req.AppName,
			Username:      req.Username,
			ContainerName: containerName,
			Subdomain:     subdomain,
			FullDomain:    fmt.Sprintf("%s.%s", subdomain, m.baseDomain),
			Port:          port,
			State:         v1.AppState_APP_STATE_UPLOADING,
			EnvVars:       req.EnvVars,
			CreatedAt:     timestamppb.Now(),
			UpdatedAt:     timestamppb.Now(),
		}
	}

	// Save initial state
	if err := m.store.Save(ctx, app); err != nil {
		return nil, nil, fmt.Errorf("failed to save app: %w", err)
	}

	// Extract and analyze source tarball
	files, err := m.extractSourceFiles(req.SourceTarball)
	if err != nil {
		app.State = v1.AppState_APP_STATE_FAILED
		app.ErrorMessage = fmt.Sprintf("failed to extract source: %v", err)
		if saveErr := m.store.Save(ctx, app); saveErr != nil {
			log.Printf("[Manager] failed to persist FAILED state for app %s: %v", app.Name, saveErr)
		}
		return nil, nil, fmt.Errorf("failed to extract source: %w", err)
	}

	// Detect language or use provided Dockerfile
	var detectedLang *v1.DetectedLanguage
	hasDockerfile := m.hasDockerfile(files, req.DockerfilePath)

	if hasDockerfile {
		detectedLang = &v1.DetectedLanguage{
			Name:               "Custom",
			DockerfileProvided: true,
		}
	} else {
		// Detect language using buildpack system
		lang, version, err := m.builder.detector.Detect(files)
		if err != nil {
			app.State = v1.AppState_APP_STATE_FAILED
			app.ErrorMessage = fmt.Sprintf("failed to detect language: %v", err)
			if saveErr := m.store.Save(ctx, app); saveErr != nil {
				log.Printf("[Manager] failed to persist FAILED state for app %s: %v", app.Name, saveErr)
			}
			return nil, nil, fmt.Errorf("failed to detect language: %w", err)
		}
		detectedLang = &v1.DetectedLanguage{
			Name:               lang,
			Version:            version,
			DockerfileProvided: false,
		}
	}

	// Update app state to building
	app.State = v1.AppState_APP_STATE_BUILDING
	app.UpdatedAt = timestamppb.Now()
	if err := m.store.Save(ctx, app); err != nil {
		return nil, nil, fmt.Errorf("failed to save app state: %w", err)
	}

	// Upload source to container
	appDir := fmt.Sprintf("/home/%s/apps/%s", req.Username, req.AppName)
	if err := m.uploadSource(ctx, containerName, appDir, req.SourceTarball); err != nil {
		app.State = v1.AppState_APP_STATE_FAILED
		app.ErrorMessage = fmt.Sprintf("failed to upload source: %v", err)
		if saveErr := m.store.Save(ctx, app); saveErr != nil {
			log.Printf("[Manager] failed to persist FAILED state for app %s: %v", app.Name, saveErr)
		}
		return nil, nil, fmt.Errorf("failed to upload source: %w", err)
	}

	// Build the container image using Podman
	buildReq := &BuildRequest{
		ContainerName:   containerName,
		AppName:         req.AppName,
		Username:        req.Username,
		SourceDir:       appDir,
		DockerfilePath:  req.DockerfilePath,
		Port:            int(port),
		Files:           files,
		BuildpackOpts:   req.Buildpack,
		GenerateIfNoDoc: !hasDockerfile,
	}

	buildResult, err := m.builder.Build(ctx, buildReq)
	if err != nil {
		app.State = v1.AppState_APP_STATE_FAILED
		app.ErrorMessage = fmt.Sprintf("build failed: %v", err)
		if saveErr := m.store.Save(ctx, app); saveErr != nil {
			log.Printf("[Manager] failed to persist FAILED state for app %s: %v", app.Name, saveErr)
		}
		return nil, nil, fmt.Errorf("build failed: %w", err)
	}

	app.ContainerImage = buildResult.ImageTag

	// Run the Podman container
	if err := m.runContainer(ctx, containerName, app, req.EnvVars); err != nil {
		app.State = v1.AppState_APP_STATE_FAILED
		app.ErrorMessage = fmt.Sprintf("failed to run container: %v", err)
		if saveErr := m.store.Save(ctx, app); saveErr != nil {
			log.Printf("[Manager] failed to persist FAILED state for app %s: %v", app.Name, saveErr)
		}
		return nil, nil, fmt.Errorf("failed to run container: %w", err)
	}

	// Configure reverse proxy
	if err := m.proxy.AddRoute(subdomain, containerInfo.IPAddress, int(port)); err != nil {
		// Log but don't fail - app is running, just not publicly accessible
		app.ErrorMessage = fmt.Sprintf("warning: proxy configuration failed: %v", err)
	}

	// Update final state
	app.State = v1.AppState_APP_STATE_RUNNING
	app.DeployedAt = timestamppb.Now()
	app.UpdatedAt = timestamppb.Now()
	if err := m.store.Save(ctx, app); err != nil {
		return nil, nil, fmt.Errorf("failed to save final state: %w", err)
	}

	return app, detectedLang, nil
}

// StopApp stops a running application
func (m *Manager) StopApp(ctx context.Context, username, appName string) (*v1.App, error) {
	app, err := m.store.GetByName(ctx, username, appName)
	if err != nil {
		return nil, fmt.Errorf("app not found: %w", err)
	}

	if app.State != v1.AppState_APP_STATE_RUNNING {
		return nil, fmt.Errorf("app is not running (current state: %s)", app.State.String())
	}

	// Stop the Podman container inside the user's LXC container
	podmanContainerName := m.podmanContainerName(app)
	stopCmd := []string{"podman", "stop", podmanContainerName}
	if err := m.incusClient.Exec(app.ContainerName, stopCmd); err != nil {
		return nil, fmt.Errorf("failed to stop podman container: %w", err)
	}

	// Update state
	app.State = v1.AppState_APP_STATE_STOPPED
	app.UpdatedAt = timestamppb.Now()
	if err := m.store.Save(ctx, app); err != nil {
