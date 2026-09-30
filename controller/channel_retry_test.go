package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveSameChannelRetryPolicyDisabledIsNoop(t *testing.T) {
	c := newChannelRetryTestContext(t)
	c.Set("specific_channel_id", "18")
	_, enabled := resolveSameChannelRetryPolicy(c, types.RelayFormatOpenAI, "source", "model-a")
	require.False(t, enabled)

	_, enabled = resolveSameChannelRetryPolicy(c, types.RelayFormatOpenAIRealtime, "source", "model-a")
	require.False(t, enabled)
}

func TestShouldRetrySameChannelTerminationRules(t *testing.T) {
	policy := resolvedChannelRetryPolicy{maxRetries: 2}
	ranges, err := operationRetryRanges("429,500-503")
	require.NoError(t, err)
	policy.ranges = ranges
	info := &relaycommon.RelayInfo{}

	newUpstreamError := func(status int) *types.NewAPIError {
		err := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, status)
		err.SetUpstreamStatusCode(status)
		return err
	}

	c := newChannelRetryTestContext(t)
	require.True(t, shouldRetrySameChannel(c, info, policy, newUpstreamError(429), 0))
	require.True(t, shouldRetrySameChannel(c, info, policy, newUpstreamError(503), 1))
	require.False(t, shouldRetrySameChannel(c, info, policy, newUpstreamError(429), 2))
	require.False(t, shouldRetrySameChannel(c, info, policy, newUpstreamError(400), 0))

	withoutUpstreamStatus := types.NewOpenAIError(errors.New("network failed"), types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	require.False(t, shouldRetrySameChannel(c, info, policy, withoutUpstreamStatus, 0))

	_, writeErr := c.Writer.Write([]byte("partial response"))
	require.NoError(t, writeErr)
	require.False(t, shouldRetrySameChannel(c, info, policy, newUpstreamError(429), 0))
	info.ChannelRetryIndex = 2
	require.False(t, shouldEnterGroupFallback(c, info, policy, newUpstreamError(429)))

	c = newChannelRetryTestContext(t)
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	require.False(t, shouldRetrySameChannel(c, info, policy, newUpstreamError(429), 0))
	require.False(t, shouldEnterGroupFallback(c, info, policy, newUpstreamError(429)))

	c = newChannelRetryTestContext(t)
	info.ChannelRetryIndex = 2
	require.True(t, shouldEnterGroupFallback(c, info, policy, newUpstreamError(429)))
	info.ChannelRetryIndex = 1
	require.False(t, shouldEnterGroupFallback(c, info, policy, newUpstreamError(429)))
	require.False(t, shouldEnterGroupFallback(c, info, policy, newUpstreamError(400)))
}

func TestTaskRetryUsesOriginalUpstreamStatus(t *testing.T) {
	ranges, err := operationRetryRanges("429,502-504")
	require.NoError(t, err)
	policy := resolvedChannelRetryPolicy{maxRetries: 2, ranges: ranges}
	c := newChannelRetryTestContext(t)
	info := &relaycommon.RelayInfo{ChannelRetryIndex: 2}

	taskErr := &dto.TaskError{
		Error:              errors.New("rate limited"),
		StatusCode:         http.StatusBadGateway,
		UpstreamStatusCode: http.StatusTooManyRequests,
	}
	require.True(t, shouldRetrySameChannelTask(c, info, policy, taskErr, 0))
	require.True(t, shouldEnterTaskGroupFallback(c, info, policy, taskErr))

	taskErr.UpstreamStatusCode = 0
	require.False(t, shouldRetrySameChannelTask(c, info, policy, taskErr, 0))
	taskErr.UpstreamStatusCode = http.StatusTooManyRequests
	taskErr.LocalError = true
	require.False(t, shouldRetrySameChannelTask(c, info, policy, taskErr, 0))
}

func TestExecuteSameChannelRetryStopsOnSuccessAndExhaustion(t *testing.T) {
	ranges, err := operationRetryRanges("429")
	require.NoError(t, err)
	policy := resolvedChannelRetryPolicy{maxRetries: 2, ranges: ranges}

	newRateLimitError := func() *types.NewAPIError {
		err := types.NewOpenAIError(errors.New("rate limited"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)
		err.SetUpstreamStatusCode(http.StatusTooManyRequests)
		return err
	}

	t.Run("success stops immediately", func(t *testing.T) {
		c := newChannelRetryTestContext(t)
		common.SetContextKey(c, constant.ContextKeyChannelId, 18)
		common.SetContextKey(c, constant.ContextKeyChannelKey, "same-key")
		info := &relaycommon.RelayInfo{}
		calls := 0
		got := executeSameChannelRetry(c, info, policy, true, func(retryCount int) *types.NewAPIError {
			calls++
			require.Equal(t, 18, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
			require.Equal(t, "same-key", common.GetContextKeyString(c, constant.ContextKeyChannelKey))
			if retryCount == 1 {
				return nil
			}
			return newRateLimitError()
		}, func() bool { return true })
		require.Nil(t, got)
		require.Equal(t, 2, calls)
		require.Equal(t, 1, info.ChannelRetryIndex)
	})

	t.Run("retry budget exhausted", func(t *testing.T) {
		c := newChannelRetryTestContext(t)
		info := &relaycommon.RelayInfo{}
		calls := 0
		got := executeSameChannelRetry(c, info, policy, true, func(retryCount int) *types.NewAPIError {
			calls++
			return newRateLimitError()
		}, func() bool { return true })
		require.NotNil(t, got)
		require.Equal(t, 3, calls)
		require.Equal(t, 2, info.ChannelRetryIndex)
	})

	t.Run("disabled executes once", func(t *testing.T) {
		c := newChannelRetryTestContext(t)
		info := &relaycommon.RelayInfo{}
		calls := 0
		got := executeSameChannelRetry(c, info, policy, false, func(retryCount int) *types.NewAPIError {
			calls++
			return newRateLimitError()
		}, func() bool { return true })
		require.NotNil(t, got)
		require.Equal(t, 1, calls)
	})
}

func TestWaitForSameChannelRetryStopsOnCancellation(t *testing.T) {
	c := newChannelRetryTestContext(t)
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)

	done := make(chan bool, 1)
	go func() {
		done <- waitForSameChannelRetry(c)
	}()
	cancel()

	select {
	case result := <-done:
		require.False(t, result)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("retry wait did not stop after client cancellation")
	}
}

func operationRetryRanges(value string) ([]operation_setting.StatusCodeRange, error) {
	return operation_setting.ParseHTTPStatusCodeRanges(value)
}

func newChannelRetryTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}
