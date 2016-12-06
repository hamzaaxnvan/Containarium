package server

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Phase 4.7 — Postgres credential resolution from a
// secret-store source (audit C-MED-6).
//
// The audit flagged that the default Postgres password
// (`containarium`) lives in source and that operators have
// no first-class path to override it via a secret store.
// This file adds two narrow opt-ins, both mode-checked
// like the JWT token file landed in PR for Phase C-HIGH-7:
//
//   CONTAINARIUM_POSTGRES_URL_FILE
//     Full DSN in a single file. Highest precedence — when
//     set, the daemon and CLI use the file's contents
//     verbatim and ignore CONTAINARIUM_POSTGRES_URL.
//
//   CONTAINARIUM_POSTGRES_PASSWORD_FILE
//     Just the password in a single file. Useful when the
//     host/user/db are stable (auto-detect or operator
//     config) but the secret rotates.
//
// Both files are perm-checked: any non-owner read/write
// bit triggers a startup error. That keeps the audit
// contract from C-HIGH-7 consistent: anywhere we read a
// credential from disk, we refuse the world-readable
