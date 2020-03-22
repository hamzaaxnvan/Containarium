package sshconfig

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// RotateIdentity prepares an existing managed configuration for a new key.
// The caller writes it only after the server accepts the registration.
func RotateIdentity(content, identityFile string) (Generated, error) {
	if identityFile == "" || strings.ContainsAny(identityFile, "\r\n\x00") {
		return Generated{}, fmt.Errorf("invalid SSH identity path")
	}
	// OpenSSH expands percent tokens even inside quotes. Paths may contain
	// spaces or percent signs, and Windows accepts forward slashes.
	identity := strconv.Quote(strings.ReplaceAll(filepath.ToSlash(identityFile), "%", "%%"))
	var out strings.Builder
	g := Generated{}
	inside, began, ended := false, false, false
	for _, line := range strings.SplitAfter(content, "\n") {
		switch strings.TrimSpace(line) {
		case beginMarker:
			if began {
				return Generated{}, fmt.Errorf("duplicate managed SSH configuration")
			}
			inside, began = true, true
		case endMarker:
