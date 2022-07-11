package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/pkg/core/recipes"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var recipeListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List available recipes",
	Long: `List built-in recipes. Reads the embedded catalog when no --server is
given, or the daemon's catalog when --server is set.`,
	Args: cobra.NoArgs,
	RunE: runRecipeList,
