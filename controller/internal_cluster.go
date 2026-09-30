package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func InternalAuthVerify(c *gin.Context) {
	var req dto.InternalAuthVerifyRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "invalid request body",
		})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "username and password are required",
		})
		return
	}

	user := model.User{
		Username: req.Username,
		Password: req.Password,
	}
	if err := user.ValidateAndFill(); err != nil {
		status := http.StatusOK
		message := "invalid username or password"
		if errors.Is(err, model.ErrDatabase) {
			status = http.StatusInternalServerError
			message = "database error"
			common.SysLog(fmt.Sprintf("internal auth verify database error for user %s: %v", req.Username, err))
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": message,
		})
		return
	}
	if model.IsTwoFAEnabled(user.Id) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "2fa enabled users are not supported by remote password auth",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": dto.InternalAuthVerifyData{
			DomesticUserID: user.Id,
			Username:       user.Username,
			DisplayName:    user.DisplayName,
			Email:          user.Email,
			Role:           user.Role,
			Status:         user.Status,
			Group:          user.Group,
		},
	})
}

func InternalBillingImport(c *gin.Context) {
	var req dto.InternalBillingLedgerImportRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "invalid request body",
		})
		return
	}

	sourceRegion := strings.TrimSpace(req.SourceRegion)
	sourceBillingID := strings.TrimSpace(req.SourceBillingID)
	if sourceRegion == "" || sourceBillingID == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "source_region and source_billing_id are required",
		})
		return
	}

	other := req.Other
	if other == nil {
		other = map[string]interface{}{}
	}
	other["imported"] = true
	other["source_region"] = sourceRegion
	other["source_node"] = strings.TrimSpace(req.SourceNode)
	other["source_billing_id"] = sourceBillingID
	other["source_user_id"] = req.SourceUserID
	other["domestic_user_id"] = req.DomesticUserID
	exchangeRate := decimal.Zero
	if strings.TrimSpace(req.ExchangeRate) != "" {
		parsed, parseErr := decimal.NewFromString(strings.TrimSpace(req.ExchangeRate))
		if parseErr != nil || parsed.IsNegative() {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid exchange_rate"})
			return
		}
		exchangeRate = parsed
	}
	amountCNYReference := decimal.Zero
	if strings.TrimSpace(req.AmountCNYReference) != "" {
		parsed, parseErr := decimal.NewFromString(strings.TrimSpace(req.AmountCNYReference))
		if parseErr != nil || parsed.IsNegative() {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid amount_cny_reference"})
			return
		}
		amountCNYReference = parsed
	}

	importedBillingID := sourceRegion + ":" + sourceBillingID
	if err := model.UpsertBillingLedger(model.BillingLedgerUpsertParams{
		BillingID:               importedBillingID,
		BillingType:             req.BillingType,
		SourceRequestID:         req.SourceRequestID,
		SourceTaskID:            req.SourceTaskID,
		UserID:                  req.DomesticUserID,
		Username:                req.Username,
		TokenID:                 req.TokenID,
		TokenName:               req.TokenName,
		UserGroup:               req.UserGroup,
		ChannelID:               req.ChannelID,
		ChannelName:             req.ChannelName,
		SupplierName:            req.SupplierName,
		ServerRegion:            sourceRegion,
		ModelName:               req.ModelName,
		UpstreamModelName:       req.UpstreamModel,
		Quota:                   req.Quota,
		Currency:                req.Currency,
		ExchangeRate:            exchangeRate,
		AmountCNYReference:      amountCNYReference,
		SkipCNYReferenceCapture: true,
		Status:                  req.Status,
		UsageSource:             req.UsageSource,
		IsStream:                req.IsStream,
		PromptTokens:            req.PromptTokens,
		CompletionTokens:        req.CompletionTokens,
		RequestHash:             req.RequestHash,
		ResponseHash:            req.ResponseHash,
		CreatedAt:               req.CreatedAt,
		SettledAt:               req.SettledAt,
		Other:                   other,
	}); err != nil {
		common.SysLog("internal billing import failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "billing import failed",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"billing_id":        importedBillingID,
			"source_region":     sourceRegion,
			"source_billing_id": sourceBillingID,
		},
	})
}
