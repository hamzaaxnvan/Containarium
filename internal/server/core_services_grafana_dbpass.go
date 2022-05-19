package server

import (
	"context"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// grafanaDBName is the database Grafana stores its state in. EnsureVictoriaMetrics
// creates it, owned by the core Postgres role.
const grafanaDBName = "grafana"

// pgPingArgs is what a Postgres connection test needs. Kept as a struct so the
// test seam on CoreServices has one stable, typed shape.
type pgPingArgs struct {
	Host     string // host:port, as Grafana's ini carries it
	User     string
	Password string
	Database string
}

// pgxPing is the production connection test: it opens one real connection with
// the given credentials and pings it. Credentials go through url.UserPassword,
// so a password with reserved characters is escaped, not interpreted.
func pgxPing(ctx context.Context, a pgPingArgs) error {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(a.User, a.Password),
		Host:   a.Host,
		Path:   "/" + a.Database,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("connect_timeout", "5")
	u.RawQuery = q.Encode()

	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	return conn.Ping(ctx)
}

// grafanaIniValue renders a value for grafana.ini. Grafana reads an unquoted
// `#` or `;` as the start of a comment, so a password containing either (or a
// quote, or leading/trailing space) is wrapped in triple quotes, which Grafana
// reads literally.
func grafanaIniValue(v string) string {
	if strings.ContainsAny(v, "#;\"`") || v != strings.TrimSpace(v) {
		return `"""` + v + `"""`
	}
	return v
}

