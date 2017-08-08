//go:build !windows && !containarium_client

package cmd

import "testing"

func TestInFleetCertScopeFromFlags(t *testing.T) {
	scope, err := inFleetCertScopeFromFlags("prod.example.com", []string{"api.example.com"}, []string{"example.com"})
	if err != nil {
		t.Fatalf("valid flags: %v", err)
	}
	if scope.Hostname != "prod.example.com" || len(scope.Aliases) != 1 || len(scope.BaseDomains) != 1 {
		t.Fatalf("scope = %+v, want the flag values", scope)
	}

