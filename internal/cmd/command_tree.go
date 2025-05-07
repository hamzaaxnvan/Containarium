package cmd

import (
	"sort"

	"github.com/spf13/cobra"
)

// movedStubAnnotation marks a cobra command registered by
// moved_stubs_client.go (containarium_client only). Read here so
// renderCommandTree can tag a stub's path distinctly without this
// (untagged, shared) file needing to reference anything client-only.
const movedStubAnnotation = "containarium/moved-stub"

func isMovedStub(cmd *cobra.Command) bool {
	return cmd.Annotations[movedStubAnnotation] == "true"
}
