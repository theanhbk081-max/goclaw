package pg

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type pgBrowserProxyStore struct {
	db *sql.DB
}

func NewBrowserProxyStore(db *sql.DB) *pgBrowserProxyStore {
	return &pgBrowserProxyStore{db: db}
}

func (s *pgBrowserProxyStore) List(ctx context.Context, tenantID string) ([]*store.BrowserProxy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tenant_id, name, url, username, password, geo, tags,
		        is_healthy, last_health_check, fail_count, created_at, updated_at
		 FROM browser_proxies WHERE tenant_id = $1
		 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProxies(rows)
}

func (s *pgBrowserProxyStore) Get(ctx context.Context, id string) (*store.BrowserProxy, error) {
	var p store.BrowserProxy
	var tags []string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, url, username, password, geo, tags,
		        is_healthy, last_health_check, fail_count, created_at, updated_at
		 FROM browser_proxies WHERE id = $1`, id,
	).Scan(&p.ID, &p.TenantID, &p.Name, &p.URL, &p.Username, &p.Password,
		&p.Geo, pq.Array(&tags), &p.IsHealthy, &p.LastHealthCheck, &p.FailCount,
		&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.Tags = tags
	return &p, nil
}

func (s *pgBrowserProxyStore) Create(ctx context.Context, p *store.BrowserProxy) error {
	if p.TenantID == "" {
		p.TenantID = tenantIDForInsert(ctx).String()
	}
	return s.db.QueryRowContext(ctx,
		`INSERT INTO browser_proxies (tenant_id, name, url, username, password, geo, tags)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 RETURNING id, created_at, updated_at`,
		p.TenantID, p.Name, p.URL, p.Username, p.Password, p.Geo, pq.Array(p.Tags),
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (s *pgBrowserProxyStore) Update(ctx context.Context, p *store.BrowserProxy) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE browser_proxies SET name=$2, url=$3, username=$4, password=$5,
		        geo=$6, tags=$7, updated_at=NOW()
		 WHERE id=$1`,
		p.ID, p.Name, p.URL, p.Username, p.Password, p.Geo, pq.Array(p.Tags))
	return err
}

func (s *pgBrowserProxyStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM browser_proxies WHERE id = $1`, id)
	return err
}

func (s *pgBrowserProxyStore) ListHealthy(ctx context.Context, tenantID, geo string) ([]*store.BrowserProxy, error) {
	var rows *sql.Rows
	var err error
	if geo != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, tenant_id, name, url, username, password, geo, tags,
			        is_healthy, last_health_check, fail_count, created_at, updated_at
			 FROM browser_proxies WHERE tenant_id = $1 AND is_healthy = true AND geo = $2
			 ORDER BY RANDOM()`, tenantID, geo)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, tenant_id, name, url, username, password, geo, tags,
			        is_healthy, last_health_check, fail_count, created_at, updated_at
			 FROM browser_proxies WHERE tenant_id = $1 AND is_healthy = true
			 ORDER BY RANDOM()`, tenantID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProxies(rows)
}

func (s *pgBrowserProxyStore) UpdateHealth(ctx context.Context, id string, healthy bool, failCount int) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE browser_proxies SET is_healthy=$2, fail_count=$3, last_health_check=$4, updated_at=$4
		 WHERE id=$1`,
		id, healthy, failCount, now)
	return err
}

func scanProxies(rows *sql.Rows) ([]*store.BrowserProxy, error) {
	var proxies []*store.BrowserProxy
	for rows.Next() {
		var p store.BrowserProxy
		var tags []string
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.URL, &p.Username, &p.Password,
			&p.Geo, pq.Array(&tags), &p.IsHealthy, &p.LastHealthCheck, &p.FailCount,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Tags = tags
		proxies = append(proxies, &p)
	}
	return proxies, rows.Err()
}
