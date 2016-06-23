package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestPiModelsJSON pins the rendered ~/.pi/agent/models.json against a fixture
// taken from pi's OWN documented custom-provider shape.
//
// The shape is not ours, so this test is the drift alarm the design doc asks
// for: it is sourced from @earendil-works/pi-coding-agent@0.87.1 —
// docs/models.md ("Configure a compatible endpoint"), docs/configuration.md
// (the agent directory table), and dist/core/model-config.d.ts, whose exported
// types are literally ModelsJsonProvider / ModelsJsonModel. A pi release that
// renames a field fails HERE, on a laptop, instead of on a customer's box.
func TestPiModelsJSON(t *testing.T) {
	got, err := RenderPiModelsJSON(PiModelsParams{
		Provider:    pb.GatewayProvider_GATEWAY_PROVIDER_KAFEIDO,
		GatewayBase: "http://10.0.0.1:8866/v1/model/kafeido",
		TokenEnvVar: GatewayTokenEnvVar,
		Model:       "kafeido-coder",
	})
	if err != nil {
		t.Fatalf("RenderPiModelsJSON: %v", err)
	}

	wantPath := filepath.Join("testdata", "pi_models_kafeido.json")
	want, err := os.ReadFile(wantPath) // #nosec G304 -- fixed in-package testdata path
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("rendered models.json does not match %s\n got:\n%s\nwant:\n%s", wantPath, got, want)
	}

	// It must also be valid JSON of exactly the documented shape, so a fixture
	// updated carelessly alongside a bug can't make both sides wrong together.
	var parsed struct {
		Providers map[string]struct {
			Name    string `json:"name"`
			BaseURL string `json:"baseUrl"`
			API     string `json:"api"`
			APIKey  string `json:"apiKey"`
			Models  []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("rendered models.json is not valid JSON: %v", err)
	}
	p, ok := parsed.Providers[PiProviderID]
