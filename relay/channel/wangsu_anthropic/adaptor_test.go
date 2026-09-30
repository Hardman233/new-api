package wangsu_anthropic

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdaptorUsesDocumentedAnthropicEndpointAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.edgecloudapp.com/v2/llm",
			ApiKey:         "test-key",
		},
	}
	adaptor := &Adaptor{}

	requestURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://api.edgecloudapp.com/v2/llm/anthropic/v1/messages", requestURL)

	headers := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &headers, info))
	require.Equal(t, "test-key", headers.Get("X-Api-Key"))
	require.Equal(t, "2023-06-01", headers.Get("anthropic-version"))
	require.Empty(t, headers.Get("Authorization"))
}

func TestAdaptorAcceptsBaseURLWithAnthropicSuffix(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl: "https://aigateway.edgecloudapp.com/v2/gws/test/anthropic/",
	}}

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://aigateway.edgecloudapp.com/v2/gws/test/anthropic/v1/messages", requestURL)
}

func TestAdaptorDoRequestUsesProviderSpecificEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "test-key", r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":"message"}`))
	}))
	defer server.Close()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl: server.URL,
		ApiKey:         "test-key",
	}}

	response, err := (&Adaptor{}).DoRequest(c, info, bytes.NewBufferString(`{"model":"test"}`))
	require.NoError(t, err)
	defer response.(*http.Response).Body.Close()
	require.Equal(t, "/anthropic/v1/messages", requestPath)
}

func TestAdaptorExposesOnlyDocumentedMessagesModels(t *testing.T) {
	models := (&Adaptor{}).GetModelList()
	require.Contains(t, models, "deepseek-v4-flash")
	require.Contains(t, models, "deepseek-v4-pro-0813")
	require.Contains(t, models, "claude-sonnet-4-6")
	require.NotContains(t, models, "deepseek-r1")
	require.NotContains(t, models, "deepseek-v3.2")
}
