package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newGroupFallbackResponseTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	c.Set(string(constant.ContextKeyTokenGroup), "source")
	c.Set(string(constant.ContextKeyGroupFallbackApplied), true)
	c.Set(string(constant.ContextKeyGroupFallbackSourceGroup), "source")
	c.Set(string(constant.ContextKeyUsingGroup), "backup")
	c.Set(string(constant.ContextKeyChannelId), 42)
	c.Set(string(constant.ContextKeyChannelName), "backup-channel")
	return c, recorder
}

func TestGroupFallbackResponseWriterAddsMetadataToTaskJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, recorder := newGroupFallbackResponseTestContext()
	wrapGroupFallbackResponseWriter(c)

	c.JSON(http.StatusOK, gin.H{"id": "task_123", "status": "queued"})

	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	metadata := response["group_fallback"].(map[string]interface{})
	require.Equal(t, true, metadata["enabled"])
	require.Equal(t, "source", metadata["source_group"])
	require.Equal(t, "backup", metadata["target_group"])
	require.Equal(t, float64(42), metadata["channel_id"])
	require.Equal(t, "backup-channel", metadata["channel_name"])
}

func TestAuditWriterCapturesSameFallbackJSONReturnedToClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, recorder := newGroupFallbackResponseTestContext()
	wrapGroupFallbackResponseWriter(c)
	auditWriter := &auditResponseWriter{
		ResponseWriter: c.Writer,
		context:        c,
		maxBytes:       64 << 10,
	}
	c.Writer = auditWriter

	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": gin.H{"code": "model_not_found", "message": "target unavailable"},
	})

	require.JSONEq(t, recorder.Body.String(), auditWriter.buf.String())
	require.Contains(t, auditWriter.buf.String(), `"group_fallback"`)
	require.Contains(t, auditWriter.buf.String(), `"channel_id":42`)
}

func TestGroupFallbackResponseWriterLeavesNonJSONUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, recorder := newGroupFallbackResponseTestContext()
	wrapGroupFallbackResponseWriter(c)

	c.Data(http.StatusOK, "image/png", []byte("binary-response"))

	require.Equal(t, "binary-response", recorder.Body.String())
}

func TestAppendGroupFallbackAuditOtherPreservesExistingData(t *testing.T) {
	c, _ := newGroupFallbackResponseTestContext()
	updated := appendGroupFallbackAuditOther(c, `{"existing":"value"}`)

	var other map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(updated), &other))
	require.Equal(t, "value", other["existing"])
	metadata := other["group_fallback"].(map[string]interface{})
	require.Equal(t, true, metadata["enabled"])
	require.Equal(t, "backup", metadata["target_group"])
	require.Equal(t, float64(42), metadata["channel_id"])
	require.Equal(t, "backup-channel", metadata["channel_name"])
	require.Equal(t, "source", common.GetContextKeyString(c, constant.ContextKeyTokenGroup))
}
