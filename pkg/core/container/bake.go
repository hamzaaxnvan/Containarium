package container

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/footprintai/containarium/pkg/version"
)

// Baked base images (#1037).
//
// Every create used to repeat identical, network-bound provisioning inside
// the fresh instance (apt update, podman repo + install, service enablement)
// — the dominant cost of a multi-minute create. BakeBaseImage runs that
// provisioning ONCE into a local image under a deterministic alias;
// Manager.Create then clones the baked image and skips the install step for
// any stackless create whose (source image, podman) matches what was baked.
//
// The bake reuses installPackages itself — there is no parallel provisioning
// implementation to drift. That works because installPackages is
// user-independent by contract (the per-user podman durability step lives in
// Create): keep it that way, or bakes and full creates will diverge.
//
// Image properties record what a bake contains; the create fast-path matches
// on them, so a bake for a different source image or podman setting is never
// silently reused. Re-bake on a schedule to pick up security updates —
// PublishImage re-points the alias and reaps the replaced image.

const (
	bakedPropBaked         = "containarium.baked"
	bakedPropSource        = "containarium.baked_source"
	bakedPropPodman        = "containarium.baked_podman"
	bakedPropFamily        = "containarium.baked_family"
	bakedPropAt            = "containarium.baked_at"
	bakedPropDaemonVersion = "containarium.baked_daemon_version"
)

// BakedImageAliasFor returns the deterministic local alias for a baked base
// image of the given source image, e.g. "images:ubuntu/24.04" →
// "containarium-base-images-ubuntu-24-04". The alias must stay free of ":"
// and "/" so the incus client resolves it as a LOCAL alias (see
// parseImageSource).
func BakedImageAliasFor(image string) string {
	s := strings.ToLower(image)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, s)
	return "containarium-base-" + strings.Trim(s, "-")
}

// bakedImageMatches reports whether a baked image's properties cover a
// create request for (image, enablePodman). Strict equality on both axes: a
// bake for a different source image or without podman must never be
// silently substituted.
func bakedImageMatches(props map[string]string, image string, enablePodman bool) bool {
	return props[bakedPropBaked] == "true" &&
		props[bakedPropSource] == image &&
		props[bakedPropPodman] == strconv.FormatBool(enablePodman)
}

// BakeOptions configures a base-image bake.
type BakeOptions struct {
	// Image is the source image to bake from (same format Create accepts,
	// default images:ubuntu/24.04). OSType, when set, takes precedence —
	// same rule as Create.
	Image        string
	OSType       pb.OSType
	EnablePodman bool
	Verbose      bool
}

