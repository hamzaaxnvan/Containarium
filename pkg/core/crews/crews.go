// Package crews provides the built-in catalog of crews. A crew is a
// collaborating set of skills bound to a task purpose, with a topology
// describing how they are wired. It mirrors pkg/core/skills and
// pkg/core/recipes: the catalog ships as embedded YAML and is exposed as
// strongly-typed pb.Crew values.
//
// This catalog is the generic mechanism only — the reference crew is
// deliberately task-agnostic. Opinionated/domain crews ship outside this repo.
package crews

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/footprintai/containarium/pkg/core/catalogsig"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"gopkg.in/yaml.v3"
)

//go:embed *.yaml
var embeddedFS embed.FS

// topologyByName maps the YAML topology string to the proto enum. Keeping the
// mapping here (not a bare string) upholds the strong-typing convention.
var topologyByName = map[string]pb.CrewTopology{
	"pipeline":     pb.CrewTopology_CREW_TOPOLOGY_PIPELINE,
	"orchestrator": pb.CrewTopology_CREW_TOPOLOGY_ORCHESTRATOR,
	"freeform":     pb.CrewTopology_CREW_TOPOLOGY_FREEFORM,
}

// roleByName maps the YAML role string to the proto enum (#2799).
var roleByName = map[string]pb.CrewRole{
	"coder":         pb.CrewRole_CREW_ROLE_CODER,
	"pack_producer": pb.CrewRole_CREW_ROLE_PACK_PRODUCER,
	"reviewer":      pb.CrewRole_CREW_ROLE_REVIEWER,
}

// crewDef is the YAML shape of a crew.
type crewDef struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Topology    string   `yaml:"topology"`
	SkillIDs    []string `yaml:"skill_ids"`
	// Roles is parallel to SkillIDs: Roles[i] is the role of SkillIDs[i].
	Roles []string `yaml:"roles,omitempty"`
}

// ToProto converts a crewDef to its pb.Crew representation.
func (c *crewDef) ToProto() *pb.Crew {
	var roles []pb.CrewRole
	for _, r := range c.Roles {
		roles = append(roles, roleByName[r])
	}
	return &pb.Crew{
		Id:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		Topology:    topologyByName[c.Topology],
		SkillIds:    c.SkillIDs,
		Roles:       roles,
	}
}

type config struct {
	Crews []crewDef `yaml:"crews"`
}

// Manager holds the loaded crew catalog.
type Manager struct {
	crews []*pb.Crew
	mu    sync.RWMutex
}

var (
	defaultManager *Manager
	once           sync.Once
)

// New creates an empty crew manager.
func New() *Manager { return &Manager{} }

// GetDefault returns the process-wide manager backed by the embedded catalog.
func GetDefault() *Manager {
	once.Do(func() {
		defaultManager = New()
		if err := defaultManager.LoadEmbedded(); err != nil {
			defaultManager.crews = nil
		}
	})
	return defaultManager
}

// LoadEmbedded loads the built-in crews.yaml bundled into the binary.
func (m *Manager) LoadEmbedded() error {
	data, err := embeddedFS.ReadFile("crews.yaml")
	if err != nil {
		return fmt.Errorf("read embedded crews: %w", err)
	}
	return m.LoadFromBytes(data)
}

// LoadFromBytes parses and validates a YAML crew catalog.
func (m *Manager) LoadFromBytes(data []byte) error {
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse crews YAML: %w", err)
	}

	loaded := make([]*pb.Crew, 0, len(cfg.Crews))
	seen := map[string]bool{}
	for i := range cfg.Crews {
		def := &cfg.Crews[i]
		if err := validate(def); err != nil {
			return err
		}
		if seen[def.ID] {
			return fmt.Errorf("duplicate crew id: %s", def.ID)
		}
		seen[def.ID] = true
		loaded = append(loaded, def.ToProto())
