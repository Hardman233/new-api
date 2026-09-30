package controller

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	channelRetryDelay            = 500 * time.Millisecond
	ginKeyChannelRetryActive     = "same_channel_retry_active"
	ginKeyChannelRetryCount      = "same_channel_retry_count"
	ginKeyChannelRetryMaxRetries = "same_channel_retry_max_retries"
)

type resolvedChannelRetryPolicy struct {
	maxRetries int
	ranges     []operation_setting.StatusCodeRange
}

func resolveSameChannelRetryPolicy(c *gin.Context, relayFormat types.RelayFormat, sourceGroup, modelName string) (resolvedChannelRetryPolicy, bool) {
	if c == nil || relayFormat == types.RelayFormatOpenAIRealtime {
		return resolvedChannelRetryPolicy{}, false
	}
	if common.GetContextKeyBool(c, constant.ContextKeyGroupFallbackApplied) || c.GetInt("youchuan_locked_channel_id") != 0 {
		return resolvedChannelRetryPolicy{}, false
	}
	if _, specificChannel := c.Get("specific_channel_id"); specificChannel {
		return resolvedChannelRetryPolicy{}, false
	}
	sourceGroup = strings.TrimSpace(sourceGroup)
	if sourceGroup == "" {
		sourceGroup = common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	}
	if sourceGroup == "" {
		sourceGroup = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	_, groupPolicy, enabled := service.ResolveGroupFallbackRetry(sourceGroup, modelName)
	if !enabled || groupPolicy.MaxRetries <= 0 {
		return resolvedChannelRetryPolicy{}, false
	}
	ranges, err := operation_setting.ParseHTTPStatusCodeRanges(groupPolicy.HTTPStatusCodes)
	if err != nil || len(ranges) == 0 {
		return resolvedChannelRetryPolicy{}, false
	}
	maxRetries := groupPolicy.MaxRetries
	if maxRetries > service.MaxGroupFallbackRetries {
		maxRetries = service.MaxGroupFallbackRetries
	}
	return resolvedChannelRetryPolicy{maxRetries: maxRetries, ranges: ranges}, true
}

func isSameChannelRetryCandidate(c *gin.Context, info *relaycommon.RelayInfo, policy resolvedChannelRetryPolicy, err *types.NewAPIError) bool {
	if c == nil || info == nil || err == nil {
		return false
	}
	if c.Request == nil || c.Request.Context().Err() != nil || c.Writer.Written() {
		return false
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	statusCode := err.GetUpstreamStatusCode()
	if statusCode < 300 || statusCode > 599 {
		return false
	}
	for _, statusRange := range policy.ranges {
		if statusCode >= statusRange.Start && statusCode <= statusRange.End {
			return true
		}
	}
	return false
}

func shouldRetrySameChannel(c *gin.Context, info *relaycommon.RelayInfo, policy resolvedChannelRetryPolicy, err *types.NewAPIError, retryCount int) bool {
	return retryCount < policy.maxRetries && isSameChannelRetryCandidate(c, info, policy, err)
}

func shouldEnterGroupFallback(c *gin.Context, info *relaycommon.RelayInfo, policy resolvedChannelRetryPolicy, err *types.NewAPIError) bool {
	return info != nil && info.ChannelRetryIndex >= policy.maxRetries && isSameChannelRetryCandidate(c, info, policy, err)
}

func taskErrorAsAPIError(taskErr *dto.TaskError) *types.NewAPIError {
	if taskErr == nil {
		return nil
	}
	underlying := taskErr.Error
	if underlying == nil {
		underlying = errors.New(taskErr.Message)
	}
	apiErr := types.NewOpenAIError(underlying, types.ErrorCodeBadResponseStatusCode, taskErr.StatusCode)
	if taskErr.LocalError {
		apiErr = types.NewErrorWithStatusCode(underlying, types.ErrorCodeBadResponseStatusCode, taskErr.StatusCode, types.ErrOptionWithSkipRetry())
	}
	apiErr.SetUpstreamStatusCode(taskErr.UpstreamStatusCode)
	return apiErr
}

func shouldRetrySameChannelTask(c *gin.Context, info *relaycommon.RelayInfo, policy resolvedChannelRetryPolicy, taskErr *dto.TaskError, retryCount int) bool {
	return shouldRetrySameChannel(c, info, policy, taskErrorAsAPIError(taskErr), retryCount)
}

func shouldEnterTaskGroupFallback(c *gin.Context, info *relaycommon.RelayInfo, policy resolvedChannelRetryPolicy, taskErr *dto.TaskError) bool {
	return shouldEnterGroupFallback(c, info, policy, taskErrorAsAPIError(taskErr))
}

func waitForSameChannelRetry(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	timer := time.NewTimer(channelRetryDelay)
	defer timer.Stop()
	select {
	case <-c.Request.Context().Done():
		return false
	case <-timer.C:
		return true
	}
}

func executeSameChannelRetry(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	policy resolvedChannelRetryPolicy,
	enabled bool,
	attempt func(retryCount int) *types.NewAPIError,
	wait func() bool,
) *types.NewAPIError {
	if attempt == nil {
		return types.NewError(errors.New("same-channel retry attempt function is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if wait == nil {
		wait = func() bool { return waitForSameChannelRetry(c) }
	}
	if c != nil {
		c.Set(ginKeyChannelRetryActive, enabled)
		c.Set(ginKeyChannelRetryCount, 0)
		c.Set(ginKeyChannelRetryMaxRetries, policy.maxRetries)
	}
	for retryCount := 0; ; retryCount++ {
		if info != nil {
			info.ChannelRetryIndex = retryCount
		}
		if c != nil {
			c.Set(ginKeyChannelRetryCount, retryCount)
		}
		err := attempt(retryCount)
		if err == nil {
			return nil
		}
		if info != nil {
			info.LastError = err
		}
		if !enabled || !shouldRetrySameChannel(c, info, policy, err, retryCount) {
			return err
		}
		channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		logger.LogInfo(c, fmt.Sprintf("same-channel retry: channel=%d retry=%d/%d reason=%s", channelID, retryCount+1, policy.maxRetries, sameChannelRetryReason(err)))
		if !wait() {
			return err
		}
	}
}

func sameChannelRetryReason(err *types.NewAPIError) string {
	if err == nil || err.GetUpstreamStatusCode() == 0 {
		return ""
	}
	return fmt.Sprintf("http_status:%d", err.GetUpstreamStatusCode())
}
