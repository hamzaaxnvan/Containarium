package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/footprintai/containarium/internal/tracker"
)

var _ tracker.WriterProvider = (*Adapter)(nil)

// resolveSelfUser resolves the credential's own GitLab user via GET
// /user — shared by AssignIfUnassigned (needs the numeric id) and
// WhoAmI (needs the username).
func (a *Adapter) resolveSelfUser(ctx context.Context, apiBase, token string) (userResponse, error) {
	var me userResponse
	err := a.get(ctx, apiBase+"/user", token, &me)
	return me, err
}

// WhoAmI resolves conn's credential to its own GitLab username.
func (a *Adapter) WhoAmI(ctx context.Context, conn tracker.Conn) (string, error) {
	me, err := a.resolveSelfUser(ctx, apiBaseURL(conn.BaseURL), conn.Credential)
	if err != nil {
		return "", err
	}
	return me.Username, nil
}

// Comment posts a comment (a GitLab "note") and returns its normalized
// form.
func (a *Adapter) Comment(ctx context.Context, conn tracker.Conn, number int64, body string) (tracker.Comment, error) {
	apiBase := apiBaseURL(conn.BaseURL)
	projectPath := url.PathEscape(conn.Project)
