package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/pkg/core/recipes"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var recipeGetCmd = &cobra.Command{
	Use:   "get <recipe-id>",
	Short: "Show a recipe's definition",
	Args:  cobra.ExactArgs(1),
	RunE:  runRecipeGet,
}

func init() {
	recipeCmd.AddCommand(recipeGetCmd)
}

func runRecipeGet(cmd *cobra.Command, args []string) error {
