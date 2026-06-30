package api

import "context"

type ctxKey int

const (
	notifierCtxKey ctxKey = iota
	principalCtxKey
	logAttrsCtxKey
)

// logAttrs collects extra key/value pairs a handler wants on its rpc log line.
type logAttrs struct{ kv []any }

// withLogAttrs seeds ctx with a collector dispatch reads after the handler runs.
func withLogAttrs(ctx context.Context) (context.Context, *logAttrs) {
	la := &logAttrs{}
	return context.WithValue(ctx, logAttrsCtxKey, la), la
}

// LogAttr adds key/value pairs to the current request's rpc log line. No-op when
// ctx carries no collector (e.g. handler invoked outside the dispatch path).
func LogAttr(ctx context.Context, kv ...any) {
	if la, ok := ctx.Value(logAttrsCtxKey).(*logAttrs); ok {
		la.kv = append(la.kv, kv...)
	}
}

// WithNotifier attaches the connection's Notifier to ctx so request handlers
// dispatched over that connection can push notifications back to the client.
func WithNotifier(ctx context.Context, n Notifier) context.Context {
	return context.WithValue(ctx, notifierCtxKey, n)
}

// NotifierFrom returns the connection's Notifier, if one was attached.
func NotifierFrom(ctx context.Context) (Notifier, bool) {
	n, ok := ctx.Value(notifierCtxKey).(Notifier)
	return n, ok
}

// Principal identifies how a connection authenticated. Admin is true when the
// caller presented the gateway's master token (vs. a minted per-client token),
// gating connection-management methods.
type Principal struct {
	Admin bool
}

// WithPrincipal attaches the connection's authenticated Principal to ctx so
// handlers dispatched over that connection can enforce per-method authorization.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey, p)
}

// PrincipalFrom returns the connection's Principal; the zero value (non-admin)
// when none was attached.
func PrincipalFrom(ctx context.Context) Principal {
	p, _ := ctx.Value(principalCtxKey).(Principal)
	return p
}
