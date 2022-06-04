package alert

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// PGCredentialStateStore is the Postgres CredentialStateStore: one row per
// skill holding the last credential source the watcher saw. The source is
// stored by its proto enum NAME, so a reader of the table never needs the
// numeric mapping, and parsed back through the generated value map.
type PGCredentialStateStore struct {
	pool *pgxpool.Pool
