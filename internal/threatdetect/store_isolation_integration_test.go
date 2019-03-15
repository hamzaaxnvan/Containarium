//go:build integration

package threatdetect

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// Exercise separate test binaries: each setup used to drop the other run's
// security_findings table, even when both used the same disposable test database.
