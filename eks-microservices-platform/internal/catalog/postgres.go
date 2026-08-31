package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRepository is the production Repository.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// PoolConfig tunes the connection pool.
type PoolConfig struct {
	DSN string
	// MaxConns is per replica. The number that matters is MaxConns times the
	// replica count times the HPA's maxReplicas: that product must stay under
	// the database's max_connections, or scaling up under load exhausts
	// connections and takes down every replica at once. docs/adr/0009 works
	// through the arithmetic for this platform.
	MaxConns int32
	MinConns int32
	// MaxConnLifetime recycles connections so a failover or a resized instance
	// is picked up without a restart.
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
}

func (c *PoolConfig) withDefaults() {
	if c.MaxConns <= 0 {
		c.MaxConns = 10
	}
	if c.MinConns < 0 {
		c.MinConns = 0
	}
	if c.MaxConnLifetime <= 0 {
		c.MaxConnLifetime = 30 * time.Minute
	}
	if c.MaxConnIdleTime <= 0 {
		c.MaxConnIdleTime = 5 * time.Minute
	}
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = 5 * time.Second
	}
}

// NewPostgresRepository opens a pool and verifies connectivity.
func NewPostgresRepository(ctx context.Context, cfg PoolConfig) (*PostgresRepository, error) {
	cfg.withDefaults()

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		// The DSN carries a password, so it is never included in the error.
		return nil, fmt.Errorf("parse database DSN: %w", redact(err))
	}
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", redact(err))
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", redact(err))
	}

	return &PostgresRepository{pool: pool}, nil
}

// Close releases the pool.
func (r *PostgresRepository) Close() { r.pool.Close() }

// Ping reports database reachability for the readiness probe.
func (r *PostgresRepository) Ping(ctx context.Context) error {
	if err := r.pool.Ping(ctx); err != nil {
		return redact(err)
	}
	return nil
}

// Migrate applies the schema.
//
// It is deliberately idempotent and runs at boot rather than as a separate Job.
// For a schema this small that removes a moving part; the moment a migration
// needs to be non-additive or long-running, it belongs in a Helm pre-upgrade
// hook instead, which docs/adr/0010 explains.
func (r *PostgresRepository) Migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS devices (
    id          TEXT PRIMARY KEY,
    name        TEXT        NOT NULL,
    site        TEXT        NOT NULL,
    kind        TEXT        NOT NULL DEFAULT 'unknown',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Supports the site filter and keyset pagination in one index, so a filtered
-- page is an index range scan rather than a filter over a full scan.
CREATE INDEX IF NOT EXISTS devices_site_id_idx ON devices (site, id);
`
	if _, err := r.pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", redact(err))
	}
	return nil
}

// Create inserts a device, reporting ErrAlreadyExists on a duplicate ID.
func (r *PostgresRepository) Create(ctx context.Context, d Device) (Device, error) {
	if err := d.Validate(); err != nil {
		return Device{}, err
	}

	const q = `
INSERT INTO devices (id, name, site, kind)
VALUES ($1, $2, $3, $4)
RETURNING id, name, site, kind, created_at, updated_at`

	var out Device
	err := r.pool.QueryRow(ctx, q, d.ID, d.Name, d.Site, d.Kind).
		Scan(&out.ID, &out.Name, &out.Site, &out.Kind, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		// 23505 is unique_violation. Letting the database decide, rather than
		// checking for existence first, keeps the operation atomic: a
		// check-then-insert races with a concurrent request.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Device{}, fmt.Errorf("%w: %s", ErrAlreadyExists, d.ID)
		}
		return Device{}, fmt.Errorf("insert device: %w", redact(err))
	}
	return out, nil
}

// Get returns one device, or ErrNotFound.
func (r *PostgresRepository) Get(ctx context.Context, id string) (Device, error) {
	const q = `SELECT id, name, site, kind, created_at, updated_at FROM devices WHERE id = $1`

	var out Device
	err := r.pool.QueryRow(ctx, q, id).
		Scan(&out.ID, &out.Name, &out.Site, &out.Kind, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Device{}, fmt.Errorf("select device: %w", redact(err))
	}
	return out, nil
}

// List returns one page of devices ordered by ID.
func (r *PostgresRepository) List(ctx context.Context, opts ListOptions) (Page, error) {
	opts.Normalise()

	// One statement covering both optional filters keeps the query plan stable
	// and avoids string-concatenated SQL. Every value is a bound parameter.
	const q = `
SELECT id, name, site, kind, created_at, updated_at
FROM devices
WHERE ($1 = '' OR site = $1)
  AND ($2 = '' OR id > $2)
ORDER BY id
LIMIT $3`

	// Fetch one extra row to learn whether another page exists without a
	// second COUNT query over the whole table.
	rows, err := r.pool.Query(ctx, q, opts.Site, opts.Cursor, opts.Limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("list devices: %w", redact(err))
	}
	defer rows.Close()

	devices := make([]Device, 0, opts.Limit)
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.Site, &d.Kind, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return Page{}, fmt.Errorf("scan device: %w", redact(err))
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate devices: %w", redact(err))
	}

	page := Page{Devices: devices}
	if len(devices) > opts.Limit {
		page.Devices = devices[:opts.Limit]
		page.NextCursor = page.Devices[len(page.Devices)-1].ID
	}
	return page, nil
}

// redact strips connection details from a driver error.
//
// pgx errors can carry the host, port, user and database from the DSN. Those
// reach logs, and logs reach places the database credentials should not.
func redact(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("postgres error %s: %s", pgErr.Code, pgErr.Message)
	}
	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return errors.New("database connection failed")
	}
	return err
}
