package buildpack

import (
	"strings"
	"testing"
)

func TestDetector_Detect(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name         string
		files        []string
		wantLang     string
		wantErr      bool
		wantContains string // For error message checking
	}{
		{
			name:     "Node.js - package.json",
			files:    []string{"package.json", "index.js", "node_modules/express/index.js"},
			wantLang: "Node.js",
			wantErr:  false,
		},
		{
			name:     "Python - requirements.txt",
			files:    []string{"requirements.txt", "app.py", "models/user.py"},
			wantLang: "Python",
			wantErr:  false,
		},
		{
			name:     "Python - Pipfile",
			files:    []string{"Pipfile", "main.py"},
			wantLang: "Python",
			wantErr:  false,
		},
		{
			name:     "Python - pyproject.toml",
			files:    []string{"pyproject.toml", "src/app.py"},
			wantLang: "Python",
			wantErr:  false,
		},
		{
			name:     "Go - go.mod",
			files:    []string{"go.mod", "go.sum", "main.go", "cmd/server/main.go"},
			wantLang: "Go",
			wantErr:  false,
		},
		{
			name:     "Rust - Cargo.toml",
			files:    []string{"Cargo.toml", "Cargo.lock", "src/main.rs"},
			wantLang: "Rust",
			wantErr:  false,
		},
		{
			name:     "Ruby - Gemfile",
			files:    []string{"Gemfile", "Gemfile.lock", "app.rb"},
			wantLang: "Ruby",
			wantErr:  false,
		},
		{
			name:     "PHP - composer.json",
			files:    []string{"composer.json", "composer.lock", "index.php"},
			wantLang: "PHP",
			wantErr:  false,
		},
		{
			name:     "Static - index.html",
			files:    []string{"index.html", "styles.css", "script.js"},
			wantLang: "Static",
			wantErr:  false,
		},
		{
			name:     "Static - public/index.html",
			files:    []string{"public/index.html", "public/styles.css"},
			wantLang: "Static",
			wantErr:  false,
		},
		{
			name:         "Unknown - no recognizable files",
			files:        []string{"data.csv", "notes.txt", "random.bin"},
			wantLang:     "",
			wantErr:      true,
			wantContains: "could not detect application type",
		},
		{
			name:         "Empty file list",
			files:        []string{},
			wantLang:     "",
			wantErr:      true,
			wantContains: "could not detect application type",
		},
		{
			name:     "Node.js takes priority over Static",
			files:    []string{"package.json", "index.html", "index.js"},
			wantLang: "Node.js",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang, _, err := detector.Detect(tt.files)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Detect() expected error, got nil")
				} else if tt.wantContains != "" && !strings.Contains(err.Error(), tt.wantContains) {
					t.Errorf("Detect() error = %q, want contains %q", err.Error(), tt.wantContains)
				}
				return
			}

			if err != nil {
				t.Errorf("Detect() unexpected error: %v", err)
				return
			}

			if lang != tt.wantLang {
				t.Errorf("Detect() lang = %q, want %q", lang, tt.wantLang)
			}
		})
	}
}

func TestDetector_GenerateDockerfile(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name         string
		langName     string
		opts         GenerateOptions
		wantContains []string
		wantErr      bool
	}{
		{
			name:     "Node.js Dockerfile",
			langName: "Node.js",
			opts: GenerateOptions{
				Port:  3000,
				Files: []string{"package.json", "index.js"},
			},
			wantContains: []string{
				"FROM node:",
				"WORKDIR /app",
				"npm install --omit=dev", // No package-lock.json, so uses npm install
				"EXPOSE 3000",
			},
			wantErr: false,
