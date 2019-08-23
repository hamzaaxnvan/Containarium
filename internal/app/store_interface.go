package app

import (
	"context"

	v1 "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// AppStore defines the interface for app storage
type AppStore interface {
	// Save saves an app to the store (insert or update)
	Save(ctx context.Context, app *v1.App) error

	// GetByID retrieves an app by its ID
