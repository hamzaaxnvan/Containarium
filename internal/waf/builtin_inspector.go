package waf

import "bytes"

// builtinSig is one cleartext exploit pattern the reference inspector matches.
// The set deliberately mirrors the in-kernel Tier 2 signatures (#661), but is
// defined locally so Tier 3 stays independent of the Tier 2 control plane — the
// two can be de-duplicated once both land. Ids are stable + nonzero (echoed in
// the audit on a block).
type builtinSig struct {
	id      uint16
	name    string
