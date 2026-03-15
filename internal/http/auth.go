package http

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/crypto"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// extractBearerToken extracts a bearer token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

// tokenMatch performs a constant-time comparison of a provided token against the expected token.
// Returns true if expected is empty (no auth configured) or if tokens match.
func tokenMatch(provided, expected string) bool {
	if expected == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// extractUserID extracts the external user ID from the request header.
// Returns "" if no user ID is provided (anonymous).
// Rejects IDs exceeding MaxUserIDLength (VARCHAR(255) DB constraint).
func extractUserID(r *http.Request) string {
	id := r.Header.Get("X-GoClaw-User-Id")
	if id == "" {
		return ""
	}
	if err := store.ValidateUserID(id); err != nil {
		slog.Warn("security.user_id_too_long", "length", len(id), "max", store.MaxUserIDLength)
		return ""
	}
	return id
}

// extractAgentID determines the target agent from the request.
// Checks model field, headers, and falls back to "default".
func extractAgentID(r *http.Request, model string) string {
	// From model field: "goclaw:<agentId>" or "agent:<agentId>"
	if after, ok := strings.CutPrefix(model, "goclaw:"); ok {
		return after
	}
	if after, ok := strings.CutPrefix(model, "agent:"); ok {
		return after
	}

	// From headers
	if id := r.Header.Get("X-GoClaw-Agent-Id"); id != "" {
		return id
	}
	if id := r.Header.Get("X-GoClaw-Agent"); id != "" {
		return id
	}

	return "default"
}

// resolveAPIKey checks if the bearer token is a valid API key.
// Returns the key data and derived role, or nil if not found/expired/revoked.
func resolveAPIKey(ctx context.Context, token string, apiKeys store.APIKeyStore) (*store.APIKeyData, permissions.Role) {
	if apiKeys == nil || token == "" {
		return nil, ""
	}
	hash := crypto.HashAPIKey(token)
	key, err := apiKeys.GetByHash(ctx, hash)
	if err != nil || key == nil {
		return nil, ""
	}
	scopes := make([]permissions.Scope, len(key.Scopes))
	for i, s := range key.Scopes {
		scopes[i] = permissions.Scope(s)
	}
	go apiKeys.TouchLastUsed(context.Background(), key.ID)
	return key, permissions.RoleFromScopes(scopes)
}

// --- Package-level API key store for shared auth ---

var pkgAPIKeyStore store.APIKeyStore

// SetPackageAPIKeyStore sets the API key store used by all HTTP handler auth checks.
// Must be called once during server startup before handling requests.
func SetPackageAPIKeyStore(s store.APIKeyStore) {
	pkgAPIKeyStore = s
}

// tryAuth checks if the request is authenticated via gateway token or API key.
// Returns true if: token matches, API key is valid, or no auth is configured (token=="").
func tryAuth(r *http.Request, token string) bool {
	return tryAuthBearer(r, token, extractBearerToken(r))
}

// tryAuthBearer is like tryAuth but accepts a pre-extracted bearer token.
// Useful for handlers that also accept tokens from query params.
func tryAuthBearer(r *http.Request, token, bearer string) bool {
	if token != "" && tokenMatch(bearer, token) {
		return true
	}
	if key, _ := resolveAPIKey(r.Context(), bearer, pkgAPIKeyStore); key != nil {
		return true
	}
	return token == ""
}

// extractLocale parses the Accept-Language header and returns a supported locale.
// Falls back to "en" if no supported language is found.
func extractLocale(r *http.Request) string {
	accept := r.Header.Get("Accept-Language")
	if accept == "" {
		return i18n.DefaultLocale
	}
	// Simple parser: take the first language tag before comma or semicolon
	for part := range strings.SplitSeq(accept, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		locale := i18n.Normalize(tag)
		if locale != i18n.DefaultLocale || strings.HasPrefix(tag, "en") {
			return locale
		}
	}
	return i18n.DefaultLocale
}
