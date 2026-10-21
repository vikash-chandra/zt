package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

const (
	UserClaimsContextKey contextKey = "user_claims"
	UserIDContextKey     contextKey = "user_id"
	UsernameContextKey   contextKey = "username"
	UserRoleContextKey   contextKey = "user_role"
)

// GetClaims retrieves UserClaims from context
func GetClaims(ctx context.Context) *UserClaims {
	if val := ctx.Value(UserClaimsContextKey); val != nil {
		if claims, ok := val.(*UserClaims); ok {
			return claims
		}
	}
	return nil
}

// GetUserID retrieves user ID from context
func GetUserID(ctx context.Context) int64 {
	if val := ctx.Value(UserIDContextKey); val != nil {
		if id, ok := val.(int64); ok {
			return id
		}
	}
	return 0
}

// GetUsername retrieves username from context
func GetUsername(ctx context.Context) string {
	if val := ctx.Value(UsernameContextKey); val != nil {
		if u, ok := val.(string); ok {
			return u
		}
	}
	return "anonymous"
}

// GetRole retrieves user role from context
func GetRole(ctx context.Context) string {
	if val := ctx.Value(UserRoleContextKey); val != nil {
		if r, ok := val.(string); ok {
			return r
		}
	}
	return ""
}

// AuthMiddleware inspects incoming requests for a valid JWT session
func AuthMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Apply security headers
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-XSS-Protection", "1; mode=block")

			path := r.URL.Path

			// Whitelisted public paths that bypass authentication
			if path == "/login" ||
				path == "/api/auth/login" ||
				path == "/favicon.ico" ||
				path == "/health" ||
				strings.HasPrefix(path, "/branding/") {
				next.ServeHTTP(w, r)
				return
			}

			// Extract token from Cookie or Authorization header
			var tokenStr string
			if cookie, err := r.Cookie("vridhi_jwt"); err == nil && cookie.Value != "" {
				tokenStr = cookie.Value
			} else if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
			}

			if tokenStr == "" {
				handleUnauthorized(w, r)
				return
			}

			claims, err := ValidateJWT(tokenStr, secret)
			if err != nil {
				// Clear invalid cookie
				http.SetCookie(w, &http.Cookie{
					Name:     "vridhi_jwt",
					Value:    "",
					Path:     "/",
					MaxAge:   -1,
					HttpOnly: true,
				})
				handleUnauthorized(w, r)
				return
			}

			// Inject user claims into request context
			ctx := context.WithValue(r.Context(), UserClaimsContextKey, claims)
			ctx = context.WithValue(ctx, UserIDContextKey, claims.UserID)
			ctx = context.WithValue(ctx, UsernameContextKey, claims.Username)
			ctx = context.WithValue(ctx, UserRoleContextKey, claims.Role)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole enforces role-based access control (RBAC) on a handler
func RequireRole(allowedRoles ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			userRole := GetRole(r.Context())
			isAllowed := false

			for _, allowed := range allowedRoles {
				if strings.EqualFold(userRole, allowed) {
					isAllowed = true
					break
				}
			}

			if !isAllowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   "Forbidden: Access requires " + strings.Join(allowedRoles, " or ") + " privileges",
					"role":    userRole,
				})
				return
			}

			next(w, r)
		}
	}
}

func handleUnauthorized(w http.ResponseWriter, r *http.Request) {
	// If the client is requesting an API endpoint, return 401 JSON
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Unauthorized: Valid session required. Please sign in.",
		})
		return
	}

	// For browser navigation (e.g. /zt, /), redirect to /login
	http.Redirect(w, r, "/login", http.StatusFound)
}
