package apiv1

import (
	"context"
	"net/http"
	"strings"
)

type credentialKey struct{}

// RequireScope rejects unauthenticated callers and credentials without scope.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entry, ok := r.Context().Value(credentialKey{}).(credential)
			if !ok {
				WriteProblem(w, 401, "unauthorized", "Authentication required")
				return
			}
			// Admin implies tenant scopes, not the operator-only platform scope.
			admin := scope != "platform" && strings.Contains(entry.scopes, ",admin,")
			if scope == "" || !strings.Contains(scopes, ","+scope+",") || (!admin && !strings.Contains(entry.scopes, ","+scope+",")) {
				WriteProblem(w, 403, "forbidden", "Insufficient scope")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TenantFrom returns the authenticated file-key tenant, or empty without auth.
func TenantFrom(ctx context.Context) string {
	if entry, ok := ctx.Value(credentialKey{}).(credential); ok {
		return entry.tenant
	}
	return ""
}
