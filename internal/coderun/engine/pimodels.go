package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// pi's ~/.pi/agent/models.json, rendered from typed Go structs.
//
// This is a THIRD-PARTY schema we do not own, so every field below is sourced
// rather than invented — from @earendil-works/pi-coding-agent@0.87.1 (the
// version pi.go pins):
//
//   - docs/models.md, "Configure a compatible endpoint", which gives the
//     worked example this shape follows;
//   - docs/configuration.md, whose agent-directory table places models.json at
//     <agent-dir>/models.json with <agent-dir> defaulting to ~/.pi/agent;
//   - dist/core/model-config.d.ts, pi's own TypeBox schema, whose exported
//     types are literally ModelsJsonProvider / ModelsJsonModel. The provider
//     object there is {name?, baseUrl?, apiKey?, api?, oauth?, headers?,
//     compat?, authHeader?, models?} and a models entry is {id, name?, api?,
//     baseUrl?, …}.
//
// TestPiModelsJSON pins the rendered bytes against a fixture, so a pi release
// that renames a field fails on a laptop instead of on a customer's box.

// defaultPiAPI is the protocol assumed when no gateway provider names one: the
// OpenAI chat-completions shape. Specified rather than guessed — the PRD's
// Story 2 AC calls for "one custom OpenAI-compatible provider", and it is the
// shape Ollama, LM Studio, vLLM, SGLang and most proxies speak (pi's own
// docs/models.md, "Configure a compatible endpoint").
const defaultPiAPI = "openai-completions"

// PiProviderID is the key our provider takes in models.json's providers map.
// "containarium" rather than the upstream vendor's name on purpose: from pi's
// point of view the endpoint IS containarium's gateway, and naming it after the
// real upstream would imply the box is talking to that vendor directly.
const PiProviderID = "containarium"

// piModelsConfig is the top level of models.json.
//
// providers is a MAP keyed by provider id, not a list — pi's docs and TypeBox
// schema both agree, and the distinction matters because a map is what lets our
// entry replace itself idempotently on re-install.
type piModelsConfig struct {
	Providers map[string]piProviderConfig `json:"providers"`
}

// piProviderConfig is pi's ModelsJsonProvider, narrowed to the fields we set.
// Omitting the rest is safe — every field in pi's schema except the provider key
// itself is optional — and narrow is better than complete here: a field we do
// not set is a field pi's defaults own.
type piProviderConfig struct {
	Name    string          `json:"name"`
	BaseURL string          `json:"baseUrl"`
	API     string          `json:"api"`
	APIKey  string          `json:"apiKey"`
	Models  []piModelConfig `json:"models"`
}

// piModelConfig is pi's ModelsJsonModel, narrowed to the one required field.
type piModelConfig struct {
	ID string `json:"id"`
}

// PiShape is how one gateway provider is expressed to pi: which of pi's API
// protocols it speaks, and what has to be appended to the gateway's
// per-provider base URL to reach it.
type PiShape struct {
	// API is a value from pi-ai's KnownApi union.
	API string
	// BaseURLSuffix is appended to MintGatewayTokenResponse.base_url.
