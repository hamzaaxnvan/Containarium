package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Sprint board reads, shared by `containarium sprint` and the platform MCP
// sprint tools so both go through the same functions.
//
// Sprint board reads. The board is a mirror a hosted control plane keeps on
// each sprint umbrella issue; these calls read it, never compute it. The
// routes exist only on a hosted control plane, so against a standalone
// daemon they come back as a plain not-found API error.

// SprintMirrorState says what reading one umbrella's mirror found. The values
// are the enum names the REST gateway emits.
type SprintMirrorState string

const (
	SprintMirrorStatePresent              SprintMirrorState = "SPRINT_MIRROR_STATE_PRESENT"
	SprintMirrorStateNoMirrorYet          SprintMirrorState = "SPRINT_MIRROR_STATE_NO_MIRROR_YET"
	SprintMirrorStateRejected             SprintMirrorState = "SPRINT_MIRROR_STATE_REJECTED"
	SprintMirrorStateNotEnabled           SprintMirrorState = "SPRINT_MIRROR_STATE_NOT_ENABLED"
	SprintMirrorStateProviderNotSupported SprintMirrorState = "SPRINT_MIRROR_STATE_PROVIDER_NOT_SUPPORTED"
)

// TrackerProvider is the umbrella's tracker, as the REST gateway names it.
type TrackerProvider string

const (
	TrackerProviderGitHub TrackerProvider = "TRACKER_PROVIDER_GITHUB"
	TrackerProviderGitLab TrackerProvider = "TRACKER_PROVIDER_GITLAB"
)

// SprintIssueRef addresses a sprint by its umbrella issue. Number is an int64
// on the wire, which the gateway serializes as a JSON string.
type SprintIssueRef struct {
	Provider TrackerProvider `json:"provider"`
	Project  string          `json:"project"`
	Number   int64           `json:"number,string"`
}

// SprintItem is one issue on a board.
type SprintItem struct {
	Number       int64   `json:"number,string"`
	Title        string  `json:"title"`
	URL          string  `json:"url,omitempty"`
	Rank         int32   `json:"rank,omitempty"`
	State        string  `json:"state"`
	Blockers     []int64 `json:"blockers,omitempty"`
	OpenBlockers []int64 `json:"openBlockers,omitempty"`
	RunStatus    string  `json:"runStatus,omitempty"`
	// RunStartedAt and WaitSince are RFC 3339 timestamps, empty when unset.
	RunStartedAt string   `json:"runStartedAt,omitempty"`
	WaitSince    string   `json:"waitSince,omitempty"`
	PRURL        string   `json:"prUrl,omitempty"`
	PRState      string   `json:"prState,omitempty"`
	Attention    []string `json:"attention,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// UnmarshalJSON accepts the gateway's encoding of repeated int64 fields, which
// is an array of JSON strings (",string" only covers scalar fields).
func (i *SprintItem) UnmarshalJSON(data []byte) error {
	type plain SprintItem
	aux := struct {
		*plain
		Blockers     []json.Number `json:"blockers"`
		OpenBlockers []json.Number `json:"openBlockers"`
		Number       json.Number   `json:"number"`
	}{plain: (*plain)(i)}
