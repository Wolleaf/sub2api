package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ReadonlyUserGuard prevents readonly administrators from falling back to the
// normal user surface, including every API-key and usage endpoint.
func ReadonlyUserGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := GetUserRoleFromContext(c)
		if role != service.RoleReadonly {
			c.Next()
			return
		}

		path := strings.TrimSuffix(strings.TrimSpace(c.Request.URL.Path), "/")
		methodAllowed := c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions
		pathAllowed := path == "/api/v1/user/profile" || path == "/api/v1/auth/me"
		if !methodAllowed || !pathAllowed {
			AbortWithError(c, http.StatusForbidden, "READONLY_FORBIDDEN", "Read-only administrator access is limited to assigned account and group views")
			return
		}

		c.Next()
	}
}
