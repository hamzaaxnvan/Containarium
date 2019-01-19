package gateway

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:webui
var webUIFiles embed.FS

// ServeWebUI serves the embedded Next.js web UI
func ServeWebUI() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the requested path, removing /webui prefix
		path := strings.TrimPrefix(r.URL.Path, "/webui")
		path = strings.TrimPrefix(path, "/")
		path = strings.TrimSuffix(path, "/") // Remove trailing slash to avoid double slashes

		// Default to index.html for root path
		if path == "" {
			path = "index.html"
		}

		// Check if this is a static asset (has file extension)
		isStaticAsset := strings.Contains(path, ".") && (strings.HasPrefix(path, "_next/") ||
			strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") ||
			strings.HasSuffix(path, ".ico") || strings.HasSuffix(path, ".svg") ||
			strings.HasSuffix(path, ".png") || strings.HasSuffix(path, ".jpg") ||
			strings.HasSuffix(path, ".woff") || strings.HasSuffix(path, ".woff2"))

		// Use forward slashes for embed.FS (not filepath.Join which uses OS separators)
		// Track the actual file path found for correct content-type detection
		fullPath := "webui/" + path
		actualPath := path
		content, err := webUIFiles.ReadFile(fullPath)

		// If not found and not a static asset, try with .html extension (for Next.js routes)
		if err != nil && !isStaticAsset {
			fullPath = "webui/" + path + ".html"
			actualPath = path + ".html"
			content, err = webUIFiles.ReadFile(fullPath)
		}

		// If still not found and not a static asset, try index.html in the path directory
		if err != nil && !isStaticAsset {
			fullPath = "webui/" + path + "/index.html"
			actualPath = path + "/index.html"
			content, err = webUIFiles.ReadFile(fullPath)
		}

		// If not found in embedded files, try reading from disk (development mode)
		if err != nil {
			diskPath := filepath.Join("internal/gateway/webui", path)
			actualPath = path
