package alert

import (
	"context"
	"errors"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"testing"
	"time"
)

type episodeProber struct {
	*fakeProber
	observation  CredentialEpisodes
	ackErr       error
	acknowledged []string
}

func (p *episodeProber) CredentialEpisodes(context.Context, CredentialWatchTarget) (CredentialEpisodes, error) {
	return p.observation, nil
}
func (p *episodeProber) AcknowledgeCredentialEpisodes(_ context.Context, _ CredentialWatchTarget, ids []string) error {
	if p.ackErr != nil {
		return p.ackErr
	}
	p.acknowledged = append(p.acknowledged, ids...)
	acknowledged := map[string]bool{}
	for _, id := range ids {
		acknowledged[id] = true
	}
	var pending []CredentialEpisode
	for _, e := range p.observation.Pending {
		if !acknowledged[e.ID] {
