package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var recipeCmd = &cobra.Command{
	Use:   "recipe",
	Short: "Deploy declarative GPU/app recipes (ollama, llama.cpp, …)",
	Long: `Recipes are one-command deployments of GPU/app workloads onto a
Containarium backend. A recipe provisions a new dedicated container and runs
its image inside it, then exposes the configured ports.

  containarium recipe list
  containarium recipe get ollama
  containarium recipe deploy ollama ol1 --gpu 0 --param model=llama3 --server <host>`,
}

func init() {
