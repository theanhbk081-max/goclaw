package browser

import "context"

// browserTenantKey is a context key for passing tenant ID to browser operations.
type browserTenantKey struct{}

// browserAgentKey is a context key for passing agent key to browser operations.
type browserAgentKey struct{}

// WithTenantID returns a context with the browser tenant ID set.
// This is used to isolate browser pages per tenant via incognito contexts.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, browserTenantKey{}, tenantID)
}

// WithAgentKey returns a context with the browser agent key set.
// This is used to track which agent opened which page.
func WithAgentKey(ctx context.Context, agentKey string) context.Context {
	return context.WithValue(ctx, browserAgentKey{}, agentKey)
}

// tenantIDFromCtx extracts the tenant ID from context.
func tenantIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(browserTenantKey{}).(string); ok {
		return v
	}
	return ""
}

// agentKeyFromCtx extracts the agent key from context.
func agentKeyFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(browserAgentKey{}).(string); ok {
		return v
	}
	return ""
}

// browserSessionKey is a context key for passing session key to browser operations.
type browserSessionKey struct{}

// WithSessionKey returns a context with the browser session key set.
// This is used to track which session opened which page.
func WithSessionKey(ctx context.Context, sessionKey string) context.Context {
	return context.WithValue(ctx, browserSessionKey{}, sessionKey)
}

// sessionKeyFromCtx extracts the session key from context.
func sessionKeyFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(browserSessionKey{}).(string); ok {
		return v
	}
	return ""
}

// browserProfileNameKey is a context key for passing profile name per-request.
type browserProfileNameKey struct{}

// WithProfileName returns a context with the browser profile name set.
// Used to override the Manager's activeProfile on a per-request basis.
func WithProfileName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, browserProfileNameKey{}, name)
}

// profileNameFromCtx extracts the profile name from context.
func profileNameFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(browserProfileNameKey{}).(string); ok {
		return v
	}
	return ""
}

// browserProfileDirKey is a context key for passing profile directory to browser operations.
type browserProfileDirKey struct{}

// WithProfileDir returns a context with the browser profile directory set.
// Used by ContainerPoolEngine to route requests to the correct container.
func WithProfileDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, browserProfileDirKey{}, dir)
}

// profileDirFromCtx extracts the profile directory from context.
func profileDirFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(browserProfileDirKey{}).(string); ok {
		return v
	}
	return ""
}
