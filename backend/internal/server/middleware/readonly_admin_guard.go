package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ReadonlyAdminGuard restricts readonly administrators to an explicit set of
// purpose-built, redacted endpoints. New admin routes are denied by default.
func ReadonlyAdminGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := GetUserRoleFromContext(c)
		if role != service.RoleReadonly {
			c.Next()
			return
		}

		if !readonlyAdminMethodAllowed(c.Request.Method) || !readonlyAdminPathAllowed(c.Request.URL.Path) {
			AbortWithError(c, http.StatusForbidden, "READONLY_FORBIDDEN", "Read-only administrator access is limited to assigned account and group views")
			return
		}

		c.Next()
	}
}

func readonlyAdminMethodAllowed(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func readonlyAdminPathAllowed(path string) bool {
	const prefix = "/api/v1/admin/readonly/"
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if !strings.HasPrefix(path, prefix) {
		return false
	}

	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) == 1 {
		return parts[0] == "accounts" || parts[0] == "groups"
	}
	if len(parts) != 2 || (parts[0] != "accounts" && parts[0] != "groups") {
		return false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && id > 0
}
