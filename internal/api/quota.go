package api

import (
	"errors"
	"net/http"
	"strings"

	"cpa-usage-keeper/internal/quota"
	"github.com/gin-gonic/gin"
)

type quotaCheckRequest struct {
	AuthIndex string `json:"auth_index"`
}

func registerQuotaRoutes(router gin.IRoutes, provider QuotaProvider) {
	router.POST("/quota/check", func(c *gin.Context) {
		if provider == nil {
			writeInternalError(c, "quota provider is not configured", nil)
			return
		}

		var request quotaCheckRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "auth_index is required"})
			return
		}
		request.AuthIndex = strings.TrimSpace(request.AuthIndex)
		if request.AuthIndex == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "auth_index is required"})
			return
		}

		response, err := provider.Check(c.Request.Context(), quota.CheckRequest{AuthIndex: request.AuthIndex})
		if err != nil {
			switch {
			case errors.Is(err, quota.ErrValidation):
				c.JSON(http.StatusBadRequest, gin.H{"error": "auth_index is required"})
			case errors.Is(err, quota.ErrNotFound):
				c.JSON(http.StatusNotFound, gin.H{"error": "quota identity not found"})
			case errors.Is(err, quota.ErrUnsupportedType), errors.Is(err, quota.ErrProviderInput):
				c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "quota identity type is unsupported"})
			default:
				writeInternalError(c, "quota check failed", err)
			}
			return
		}

		c.JSON(http.StatusOK, response)
	})
}
