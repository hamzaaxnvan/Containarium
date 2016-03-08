package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// `containarium box delete` (the noun-verb form of `delete`). It is a second
// way to reach the same code, with a guard the older command does not have:
// it refuses unless --yes is given, because deleting a box removes the
// container and its disk. `containarium delete` is unchanged, so existing
// scripts keep working.

var (
	boxDeleteYes   bool
	boxDeleteForce bool

	// boxDeleteFn is the delete itself; a var so a test can observe that a
	// refused call never reaches it.
	boxDeleteFn = deleteBox
)

var boxCmd = &cobra.Command{
	Use:   "box",
