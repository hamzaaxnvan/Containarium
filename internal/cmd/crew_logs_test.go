package cmd

import (
	"bytes"
	"context"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// fakeJournalServer serves a journal the way TailRunLog does, three chunks
