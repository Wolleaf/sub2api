package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ReadonlyHandler exposes only purpose-built, redacted account and group views.
type ReadonlyHandler struct {
	service *service.ReadonlyAdminService
}

func NewReadonlyHandler(readonlyService *service.ReadonlyAdminService) *ReadonlyHandler {
	return &ReadonlyHandler{service: readonlyService}
}

func (h *ReadonlyHandler) ListGroups(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.service.ListGroups(c.Request.Context(), subject.UserID, page, pageSize, readonlyListFilter(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}

func (h *ReadonlyHandler) GetGroup(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	id, err := readonlyResourceID(c)
	if err != nil {
		response.NotFound(c, "Resource not found")
		return
	}
	item, err := h.service.GetGroup(c.Request.Context(), subject.UserID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *ReadonlyHandler) ListAccounts(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.service.ListAccounts(c.Request.Context(), subject.UserID, page, pageSize, readonlyListFilter(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}

func (h *ReadonlyHandler) GetAccount(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	id, err := readonlyResourceID(c)
	if err != nil {
		response.NotFound(c, "Resource not found")
		return
	}
	item, err := h.service.GetAccount(c.Request.Context(), subject.UserID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func readonlyListFilter(c *gin.Context) service.ReadonlyListFilter {
	return service.ReadonlyListFilter{
		Search:   trimReadonlyQuery(c.Query("search")),
		Platform: trimReadonlyQuery(c.Query("platform")),
		Status:   trimReadonlyQuery(c.Query("status")),
		Type:     trimReadonlyQuery(c.Query("type")),
	}
}

func trimReadonlyQuery(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 100 {
		return string(runes[:100])
	}
	return value
}

func readonlyResourceID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, strconv.ErrSyntax
	}
	return id, nil
}
