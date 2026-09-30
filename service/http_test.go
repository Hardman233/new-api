package service

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAppendGroupFallbackResponseMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set(string(constant.ContextKeyTokenGroup), "sd5.0")
	ctx.Set(string(constant.ContextKeyGroupFallbackApplied), true)
	ctx.Set(string(constant.ContextKeyGroupFallbackSourceGroup), "sd5.0")
	ctx.Set(string(constant.ContextKeyUsingGroup), "sd5.2")
	ctx.Set(string(constant.ContextKeyChannelId), 17)
	ctx.Set(string(constant.ContextKeyChannelName), "fallback-channel")

	response := appendGroupFallbackResponseMetadata(
		ctx,
		&http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}},
		[]byte(`{"id":"test","object":"image"}`),
	)

	var decoded struct {
		GroupFallback struct {
			Enabled     bool   `json:"enabled"`
			SourceGroup string `json:"source_group"`
			TargetGroup string `json:"target_group"`
			ChannelId   int    `json:"channel_id"`
			ChannelName string `json:"channel_name"`
		} `json:"group_fallback"`
	}
	require.NoError(t, json.Unmarshal(response, &decoded))
	require.True(t, decoded.GroupFallback.Enabled)
	require.Equal(t, "sd5.0", decoded.GroupFallback.SourceGroup)
	require.Equal(t, "sd5.2", decoded.GroupFallback.TargetGroup)
	require.Equal(t, 17, decoded.GroupFallback.ChannelId)
	require.Equal(t, "fallback-channel", decoded.GroupFallback.ChannelName)
}

func TestAppendGroupFallbackResponseMetadataLeavesJSONUnchangedWhenNotApplied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set(string(constant.ContextKeyTokenGroup), "sd5.0")
	ctx.Set(string(constant.ContextKeyUsingGroup), "sd5.0")
	original := []byte(`{"id":"test"}`)

	response := appendGroupFallbackResponseMetadata(
		ctx,
		&http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}},
		original,
	)

	require.Equal(t, original, response)
}

func TestAppendGroupFallbackResponseMetadataSkipsNonJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set(string(constant.ContextKeyTokenGroup), "sd5.0")

	data := []byte("binary response")
	require.Equal(t, data, appendGroupFallbackResponseMetadata(
		ctx,
		&http.Response{Header: http.Header{"Content-Type": []string{"image/png"}}},
		data,
	))
}
