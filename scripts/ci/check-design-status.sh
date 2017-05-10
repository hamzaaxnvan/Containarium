#!/usr/bin/env bash
# Fail when a design document has no Status line (#2558).
#
# Usage:
#   scripts/ci/check-design-status.sh [docs-root]     # default: docs
#   scripts/ci/check-design-status.sh --self-test
#
# Checked: docs/*DESIGN*.md and docs/architecture/*.md.
# A Status line is one of the first 30 lines that starts with `Status:`,
# optionally behind `> ` and/or bold/italic markers, or `| Status |` in a
