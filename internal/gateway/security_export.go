package gateway

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/security"
)

// registerSecurityExport registers the CSV export endpoint on the HTTP mux
func registerSecurityExport(mux *http.ServeMux, store *security.Store, authMW *auth.AuthMiddleware) {
	mux.HandleFunc("/v1/security/clamav-reports/export", func(w http.ResponseWriter, r *http.Request) {
		// Validate auth token from query param or Authorization header
		token := r.URL.Query().Get("token")
		if token == "" {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}
		if token == "" {
			http.Error(w, `{"error": "unauthorized: token required", "code": 401}`, http.StatusUnauthorized)
			return
		}
		if _, err := authMW.ValidateToken(token); err != nil {
			http.Error(w, `{"error": "unauthorized: invalid token", "code": 401}`, http.StatusUnauthorized)
			return
		}

		handleClamavExport(w, r, store)
	})
}

func handleClamavExport(w http.ResponseWriter, r *http.Request, store *security.Store) {
