//go:build !containarium_client

package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/pkg/core/container"
)

func installStackLocal(username, stackID string) error {
	mgr, err := container.New()
	if err != nil {
