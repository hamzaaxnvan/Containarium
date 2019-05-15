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
			if err != nil {
				// Serve index.html as fallback
				if path != "index.html" {
					fullPath = filepath.Join("swagger-ui", "index.html")
					content, err = swaggerUIFiles.ReadFile(fullPath)
					if err != nil {
						diskPath = filepath.Join("internal/gateway/swagger-ui", "index.html")
						content, err = os.ReadFile(diskPath)
						if err != nil {
							// If still not found, serve a basic HTML page with instructions
							serveBasicSwaggerUI(w, r)
							return
						}
					}
				} else {
					serveBasicSwaggerUI(w, r)
					return
				}
			}
		}

		// Determine content type
		contentType := getContentType(path)
		w.Header().Set("Content-Type", contentType)

		// If it's the HTML file, inject our OpenAPI spec URL
		if strings.HasSuffix(path, ".html") || strings.HasSuffix(path, "index.html") {
			htmlContent := string(content)
			// Replace the default Swagger Petstore URL with our spec
			htmlContent = strings.Replace(htmlContent,
				"url: \"https://petstore.swagger.io/v2/swagger.json\"",
				"url: \"/swagger.json\"",
