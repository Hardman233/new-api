package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRespondTaskErrorMarksUpstreamErrorForClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	taskErr := &dto.TaskError{
		Code:               "fail_to_fetch_task",
		Message:            "provider bad gateway",
		ErrorSource:        dto.TaskErrorSourceUpstream,
		UpstreamStatusCode: http.StatusBadGateway,
		StatusCode:         http.StatusBadGateway,
	}

	respondTaskError(ctx, taskErr)

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "fail_to_fetch_task", body["code"])
	require.Equal(t, "上游服务错误：provider bad gateway", body["message"])
	require.Equal(t, dto.TaskErrorSourceUpstream, body["error_source"])
	require.EqualValues(t, http.StatusBadGateway, body["upstream_status_code"])
}

func TestShouldRetryTaskRelayForUnsupportedChannelCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.True(t, shouldRetryTaskRelay(ctx, 7, &dto.TaskError{
		Code:       "unsupported_upstream_capability",
		StatusCode: http.StatusUnprocessableEntity,
		LocalError: true,
	}, 1))
	require.False(t, shouldRetryTaskRelay(ctx, 7, &dto.TaskError{
		Code:       "invalid_request",
		StatusCode: http.StatusUnprocessableEntity,
		LocalError: true,
	}, 1))
}
