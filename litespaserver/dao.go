package litespaserver

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// settingVersionKey is the key under which the live frontend version is stored
// in the "litespa_settings" table.
const settingVersionKey = "frontend.version"

// dao reads and writes the frontend version row in the "litespa_settings"
// key-value table. The table must exist before the dao is used; consumers
// should create it via their own migration. Required schema:
//
//	-- litespa_settings: stores frontend version state for litespaserver.
//	-- The litespaserver module in orcacommon reads/writes the served version
//	-- under this table name.
//	CREATE TABLE "litespa_settings" (
//	  "id"         TEXT PRIMARY KEY,
//	  "value"      TEXT NOT NULL,
//	  "updated_on" TIMESTAMPTZ NOT NULL DEFAULT now()
//	);
type dao struct {
	pool *pgxpool.Pool
}

// getVersion returns the stored frontend version. It returns ("", nil) when no
// row exists yet.
func (d *dao) getVersion(ctx context.Context) (string, error) {
	var version string
	err := d.pool.QueryRow(ctx,
		`SELECT value FROM litespa_settings WHERE id = $1`,
		settingVersionKey,
	).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return version, nil
}

// setVersion upserts the frontend version, refreshing updated_on on conflict.
func (d *dao) setVersion(ctx context.Context, version string) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO litespa_settings(id, value, updated_on)
		 VALUES ($1, $2, CURRENT_TIMESTAMP)
		 ON CONFLICT (id)
		 DO UPDATE SET value = $2, updated_on = CURRENT_TIMESTAMP`,
		settingVersionKey, version,
	)
	return err
}
