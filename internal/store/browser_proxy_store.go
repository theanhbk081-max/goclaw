package store

import (
	"context"
	"time"
)

// BrowserProxy represents a proxy in the pool.
type BrowserProxy struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenantId"`
	Name            string    `json:"name"`
	URL             string    `json:"url"`
	Username        string    `json:"username,omitempty"`
	Password        string    `json:"password,omitempty"` // encrypted via crypto.Encrypt
	Geo             string    `json:"geo,omitempty"`      // country code: VN, US, JP
	Tags            []string  `json:"tags,omitempty"`
	IsHealthy       bool      `json:"isHealthy"`
	FailCount       int       `json:"failCount"`
	LastHealthCheck *time.Time `json:"lastHealthCheck,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// BrowserProxyStore manages browser proxies.
type BrowserProxyStore interface {
	List(ctx context.Context, tenantID string) ([]*BrowserProxy, error)
	Get(ctx context.Context, id string) (*BrowserProxy, error)
	Create(ctx context.Context, p *BrowserProxy) error
	Update(ctx context.Context, p *BrowserProxy) error
	Delete(ctx context.Context, id string) error
	ListHealthy(ctx context.Context, tenantID, geo string) ([]*BrowserProxy, error)
	UpdateHealth(ctx context.Context, id string, healthy bool, failCount int) error
}
