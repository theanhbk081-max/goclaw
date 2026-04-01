package browser

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/crypto"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// ProxyManager manages the proxy pool: assignment, rotation, health checks.
type ProxyManager struct {
	store      store.BrowserProxyStore
	encryptKey string
	logger     *slog.Logger
	mu         sync.Mutex
	rrIndex    map[string]int // tenantID+geo → round-robin index
}

// NewProxyManager creates a ProxyManager.
func NewProxyManager(s store.BrowserProxyStore, encryptKey string, l *slog.Logger) *ProxyManager {
	if l == nil {
		l = slog.Default()
	}
	return &ProxyManager{
		store:      s,
		encryptKey: encryptKey,
		logger:     l,
		rrIndex:    make(map[string]int),
	}
}

// Assign picks a healthy proxy matching geo hint using round-robin.
func (pm *ProxyManager) Assign(ctx context.Context, tenantID, geo string) (*store.BrowserProxy, error) {
	proxies, err := pm.store.ListHealthy(ctx, tenantID, geo)
	if err != nil {
		return nil, fmt.Errorf("list healthy proxies: %w", err)
	}
	if len(proxies) == 0 {
		return nil, fmt.Errorf("no healthy proxies available (tenant=%s, geo=%s)", tenantID, geo)
	}

	pm.mu.Lock()
	key := tenantID + ":" + geo
	idx := pm.rrIndex[key] % len(proxies)
	pm.rrIndex[key] = idx + 1
	pm.mu.Unlock()

	return proxies[idx], nil
}

// FormatURL returns "protocol://user:pass@host:port" with decrypted password.
func (pm *ProxyManager) FormatURL(p *store.BrowserProxy) (string, error) {
	parsed, err := url.Parse(p.URL)
	if err != nil {
		return p.URL, nil
	}

	if p.Username != "" {
		password := p.Password
		if password != "" && pm.encryptKey != "" {
			decrypted, err := crypto.Decrypt(password, pm.encryptKey)
			if err != nil {
				return "", fmt.Errorf("decrypt proxy password: %w", err)
			}
			password = decrypted
		}
		parsed.User = url.UserPassword(p.Username, password)
	}

	return parsed.String(), nil
}

// RunHealthCheck pings all proxies for a tenant and updates health status.
func (pm *ProxyManager) RunHealthCheck(ctx context.Context, tenantID string) error {
	proxies, err := pm.store.List(ctx, tenantID)
	if err != nil {
		return err
	}

	for _, p := range proxies {
		healthy := pm.checkProxy(p)
		failCount := 0
		if !healthy {
			failCount = p.FailCount + 1
		}
		// Mark unhealthy after 3 consecutive failures
		isHealthy := failCount < 3
		if healthy {
			isHealthy = true
			failCount = 0
		}

		if err := pm.store.UpdateHealth(ctx, p.ID, isHealthy, failCount); err != nil {
			pm.logger.Warn("failed to update proxy health", "proxy", p.Name, "error", err)
		}
	}
	return nil
}

// checkProxy attempts a TCP connection to the proxy to verify reachability.
func (pm *ProxyManager) checkProxy(p *store.BrowserProxy) bool {
	parsed, err := url.Parse(p.URL)
	if err != nil {
		return false
	}
	host := parsed.Host
	if host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// List returns all proxies for a tenant.
func (pm *ProxyManager) List(ctx context.Context, tenantID string) ([]*store.BrowserProxy, error) {
	return pm.store.List(ctx, tenantID)
}

// Add creates a new proxy. Password is encrypted before storage.
func (pm *ProxyManager) Add(ctx context.Context, p *store.BrowserProxy) error {
	if p.Password != "" && pm.encryptKey != "" {
		encrypted, err := crypto.Encrypt(p.Password, pm.encryptKey)
		if err != nil {
			return fmt.Errorf("encrypt proxy password: %w", err)
		}
		p.Password = encrypted
	}
	return pm.store.Create(ctx, p)
}

// Remove deletes a proxy.
func (pm *ProxyManager) Remove(ctx context.Context, id string) error {
	return pm.store.Delete(ctx, id)
}
