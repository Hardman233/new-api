package controller

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func InternalSeedanceOpenWhiteCreateAsset(c *gin.Context) {
	var req service.SeedanceOpenWhiteCreateRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "invalid_request",
				"message": err.Error(),
			},
		})
		return
	}
	req.IncludePeers = false
	ctx, cancel := seedanceOpenWhiteServiceContext()
	defer cancel()
	if len(req.Models) > 0 {
		resp, err := service.CreateSeedanceOpenWhiteAssets(ctx, req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "invalid_request",
					"message": err.Error(),
				},
			})
			return
		}
		c.JSON(http.StatusOK, resp)
		return
	}
	resp, err := service.CreateSeedanceOpenWhiteAsset(ctx, req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"code":    "open_white_create_failed",
				"message": err.Error(),
			},
		})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func seedanceOpenWhiteServiceContext() (context.Context, context.CancelFunc) {
	timeoutSec := common.GetEnvOrDefault("OPEN_WHITE_CREATE_TIMEOUT_SEC", 600)
	if timeoutSec <= 0 {
		timeoutSec = 600
	}
	return context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
}

func GetSeedanceOpenWhiteStatus(c *gin.Context) {
	openWhiteID := strings.TrimSpace(c.Param("id"))
	if openWhiteID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "invalid_request",
				"message": "open_white_id is required",
			},
		})
		return
	}
	resp, err := service.GetSeedanceOpenWhiteResponse(openWhiteID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"code":    "open_white_not_found",
				"message": err.Error(),
			},
		})
		return
	}
	c.JSON(http.StatusOK, resp)
}
