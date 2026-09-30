package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type trackingReadCloser struct {
	*strings.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestResetStatusCode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		statusCode       int
		statusCodeConfig string
		expectedCode     int
	}{
		{
			name:             "map string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"503"}`,
			expectedCode:     503,
		},
		{
			name:             "map int value",
			statusCode:       429,
			statusCodeConfig: `{"429":503}`,
			expectedCode:     503,
		},
		{
			name:             "skip invalid string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"bad-code"}`,
			expectedCode:     429,
		},
		{
			name:             "skip status code 200",
			statusCode:       200,
			statusCodeConfig: `{"200":503}`,
			expectedCode:     200,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			newAPIError := &types.NewAPIError{
				StatusCode: tc.statusCode,
			}
			ResetStatusCode(newAPIError, tc.statusCodeConfig)
			require.Equal(t, tc.expectedCode, newAPIError.StatusCode)
		})
	}
}

func TestRelayErrorHandlerPreservesUpstreamStatusCode(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"retry later","type":"rate_limit_error","code":"rate_limit_error"}}`)),
	}

	err := RelayErrorHandler(context.Background(), resp, false)
	require.NotNil(t, err)
	require.Equal(t, http.StatusTooManyRequests, err.GetUpstreamStatusCode())

	ResetStatusCode(err, `{"429":"503"}`)
	require.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
	require.Equal(t, http.StatusTooManyRequests, err.GetUpstreamStatusCode())
}

func TestRelayErrorHandlerWithUpstreamLogPreservesErrorParsingAndClosesBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := &trackingReadCloser{Reader: strings.NewReader(`{"error":{"message":"field messages is required","type":"aigateway_error","code":"invalid_text_request"}}`)}
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     make(http.Header),
		Body:       body,
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-luna"}}

	oldEnabled := common.UpstreamResponseLogEnabled
	common.UpstreamResponseLogEnabled = false
	t.Cleanup(func() { common.UpstreamResponseLogEnabled = oldEnabled })

	err := RelayErrorHandlerWithUpstreamLog(ctx, info, resp, false)
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Contains(t, err.Error(), "field messages is required")
	require.True(t, body.closed)
}
