//go:build unit

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func readonlyGuardRouter(guard gin.HandlerFunc, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUserRole), role)
		c.Next()
	})
	router.Use(guard)
	router.Any("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return router
}

func TestReadonlyAdminGuard_ExactAllowlist(t *testing.T) {
	router := readonlyGuardRouter(ReadonlyAdminGuard(), service.RoleReadonly)
	allowed := []string{
		"/api/v1/admin/readonly/accounts",
		"/api/v1/admin/readonly/accounts/42",
		"/api/v1/admin/readonly/groups",
		"/api/v1/admin/readonly/groups/7",
	}
	for _, path := range allowed {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
			request := httptest.NewRequest(method, path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusNoContent, response.Code, "%s %s", method, path)
		}
	}
}

func TestReadonlyAdminGuard_DeniesDangerousAndUnknownRoutes(t *testing.T) {
	router := readonlyGuardRouter(ReadonlyAdminGuard(), service.RoleReadonly)
	paths := []string{
		"/api/v1/admin/accounts",
		"/api/v1/admin/accounts/data",
		"/api/v1/admin/groups/1/api-keys",
		"/api/v1/admin/groups/3/weekly-rate-limit-bypass",
		"/api/v1/admin/users/1/api-keys",
		"/api/v1/admin/users",
		"/api/v1/admin/usage",
		"/api/v1/admin/settings",
		"/api/v1/admin/proxies",
		"/api/v1/admin/audit-logs",
		"/api/v1/admin/dashboard",
		"/api/v1/admin/payment/dashboard",
		"/api/v1/admin/readonly/accounts/1/credentials",
		"/api/v1/admin/future-safe-get",
	}
	for _, path := range paths {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusForbidden, response.Code, path)
		require.Contains(t, response.Body.String(), "READONLY_FORBIDDEN", path)
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		request := httptest.NewRequest(method, "/api/v1/admin/readonly/accounts", strings.NewReader("{}"))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusForbidden, response.Code, method)
	}
}

func TestReadonlyAdminGuard_DoesNotChangeFullAdminBehavior(t *testing.T) {
	router := readonlyGuardRouter(ReadonlyAdminGuard(), service.RoleAdmin)
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/accounts/42", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
}

func TestReadonlyUserGuard_OnlyProfileAndSessionRefreshAreReadable(t *testing.T) {
	router := readonlyGuardRouter(ReadonlyUserGuard(), service.RoleReadonly)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)

	request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)

	blocked := []string{
		"/api/v1/keys",
		"/api/v1/keys/1",
		"/api/v1/usage",
		"/api/v1/subscriptions",
		"/api/v1/user/password",
		"/api/v1/user/totp/status",
		"/api/v1/payment/plans",
		"/api/v1/auth/revoke-all-sessions",
		"/api/v1/auth/oauth/bind-token",
	}
	for _, path := range blocked {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusForbidden, response.Code, path)
		require.Contains(t, response.Body.String(), "READONLY_FORBIDDEN", path)
	}

	request = httptest.NewRequest(http.MethodPut, "/api/v1/user/profile", strings.NewReader("{}"))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
}
