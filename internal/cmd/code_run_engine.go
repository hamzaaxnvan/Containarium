package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/internal/gatewayprovider"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// `code run`'s engine + credential half (#1727).
//
// Everything below the command string is untouched: process_start, tail_log, the
// resumable reader, demux, attach/status/stop. The reader only ever sees bytes
// and offsets, so it is engine-agnostic by construction — which is why #1727 is
// a change to three functions and not to the streaming core.

// prepareCodeRun reads the box's code.json, mints this run's credential if it
// needs one, writes it, and returns the command process_start should spawn
// plus the resolved engine (the caller needs it afterwards to discover this
// run's session id, #2193 — the glob it polls is engine-specific).
//
// The order matters: the credential file is on the box BEFORE process_start, so
// the run's very first action can already read it. Minting after the process
// starts would be a race the run usually loses.
func prepareCodeRun(ctx context.Context, sess *coderun.Session, box, runName string, diag io.Writer) (string, engine.Engine, error) {
	cfg, err := readBoxCodeConfig(ctx, sess, diag)
	if err != nil {
		return "", nil, err
	}

	eng, err := cfg.EngineFor()
	if err != nil {
		return "", nil, fmt.Errorf("%s on %q: %w", engine.CodeConfigPath, box, err)
	}

	if cfg.Credential == engine.KindGateway {
		if err := mintAndWriteRunToken(ctx, sess, box, runName, cfg, eng, diag); err != nil {
			return "", nil, err
		}
	}

	reportModelTrafficScanning(eng, diag)

	return eng.RunCommand(codeRunPrompt, codeRunStreamJSON, codeRunContinue, codeRunSession), eng, nil
}

// reportModelTrafficScanning prints, once at run start, whether this run's
// model responses are scanned by the inbound guardrail (#2367), so a missing
// alert is never read as "clean". Only a gateway run consults the server
// policy; a failed read is reported as unknown and never blocks the run.
func reportModelTrafficScanning(eng engine.Engine, diag io.Writer) {
	cred := eng.Credential().Kind()
	effective := pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_UNSPECIFIED
	if cred == engine.KindGateway {
		if api, done, err := newGuardrailStatusAPI(); err == nil {
			if resp, gerr := api.GetGuardrailStatus(); gerr == nil {
				effective = resp.GetResponsePolicy()
			}
			done()
		}
