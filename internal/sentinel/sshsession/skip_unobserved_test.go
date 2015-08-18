package sshsession

import (
	"reflect"
	"testing"

	"github.com/tg123/sshpiper/libplugin"
)

// In enforce mode ssh-authz-plugin returns the upstream itself and writes the
// session records (#2741). This plugin then gets PipeStart and PipeError for a
// connection whose credential it never saw, and must write nothing for it, or
// the session would be recorded twice.
