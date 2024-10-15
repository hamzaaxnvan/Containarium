package server

import (
	"fmt"
	"log"
	"strings"
)

// normalizeDNSHost lowercases h, trims surrounding spaces and one trailing dot,
// and reports whether the result is a syntactically valid DNS name. It is the
// gate between operator config and the generated dnsmasq file: anything it
// accepts contains only letters, digits, '-', '_' and '.', so it cannot smuggle
// a second directive (newline), a delimiter ('/') or a wildcard into
// raw.dnsmasq.
func normalizeDNSHost(h string) (string, bool) {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	if h == "" || len(h) > 253 {
		return "", false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			alnum := c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
			if !alnum && c != '-' && c != '_' {
				return "", false
			}
		}
	}
	return h, true
}

// bridgeDNSStore is the slice of the Incus client the upgrade cleanup needs.
type bridgeDNSStore interface {
	GetNetworkConfigValue(network, key string) (string, error)
	SetNetworkConfigValue(network, key, value string) error
}

// cleanupBridgeDNSRecord removes, once on upgrade, the bridge DNS record that
// earlier daemons wrote to the bridge's raw.dnsmasq (#2734, slice C of #2263).
// The kernel hairpin rule (#2732) replaced it. A host with no base domain,
// which includes a correctly brought-up BYOC backend, is left untouched and
// the bridge is not even read (decision Q36 on #2727). Writing only happens
// when there is something to remove, and an empty result unsets the key.
func cleanupBridgeDNSRecord(be bridgeDNSStore, bridge, baseDomain, sshHost string) error {
	if baseDomain == "" {
		return nil
	}
	current, err := be.GetNetworkConfigValue(bridge, "raw.dnsmasq")
	if err != nil {
		return fmt.Errorf("read %s raw.dnsmasq: %w", bridge, err)
	}
	stripped, changed := stripBridgeDNSRecord(current, baseDomain, sshHost)
	if !changed {
		return nil
	}
	if err := be.SetNetworkConfigValue(bridge, "raw.dnsmasq", stripped); err != nil {
		return fmt.Errorf("write %s raw.dnsmasq: %w", bridge, err)
	}
	if stripped == "" {
		log.Printf("[bridgedns] removed the old *.%s record from %s raw.dnsmasq (the key is now unset); boxes resolve the base domain upstream and reach this host's apps through the kernel hairpin rule", baseDomain, bridge)
	} else {
		log.Printf("[bridgedns] removed the old *.%s record from %s raw.dnsmasq; kept the other lines: %q", baseDomain, bridge, stripped)
	}
	return nil
}
