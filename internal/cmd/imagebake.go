//go:build !containarium_client

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/ostype"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var (
	bakeImage  string
	bakePodman bool
	bakeOSType string
)

var imageBakeCmd = &cobra.Command{
	Use:   "image-bake",
	Short: "Bake a provisioned base image so creates skip the in-container package install",
	Long: `Bake a base image (#1037): launch a throwaway container from the source
image, run the exact provisioning a create would (package repos, podman,
services — no user, no stack), and publish the result as a local image under
a deterministic alias.

Afterwards, every stackless 'containarium create' for the same image/podman
combination clones the baked image and skips the multi-minute in-container
install — creates drop from minutes to roughly clone+boot+user setup.

Re-run this on a schedule (cron/systemd timer) to pick up security updates:
a re-bake re-points the alias at the fresh image and reaps the replaced one.
Delete the alias ('incus image alias delete <alias>') to fall back to full
