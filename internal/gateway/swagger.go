package gateway

import (
	"embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed swagger-ui
var swaggerUIFiles embed.FS

// ServeSwaggerUI serves the Swagger UI
func ServeSwaggerUI(swaggerDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the requested path
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "/" {
			path = "index.html"
		}

		// Try to read from embedded files first
		fullPath := filepath.Join("swagger-ui", path)
		content, err := swaggerUIFiles.ReadFile(fullPath)

		// If not found in embedded files, try reading from disk
		if err != nil {
			// Try reading from swagger-ui directory on disk
			diskPath := filepath.Join("internal/gateway/swagger-ui", path)
			content, err = os.ReadFile(diskPath)
