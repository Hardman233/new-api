package middleware

import (
	"bytes"
	"io"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const groupFallbackResponseWriterKey = "group_fallback_response_writer"

type groupFallbackResponseWriter struct {
	gin.ResponseWriter
	context *gin.Context
}

func wrapGroupFallbackResponseWriter(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	if _, exists := c.Get(groupFallbackResponseWriterKey); exists {
		return
	}
	c.Set(groupFallbackResponseWriterKey, true)
	c.Writer = &groupFallbackResponseWriter{ResponseWriter: c.Writer, context: c}
}

func (w *groupFallbackResponseWriter) Write(data []byte) (int, error) {
	updated := transformGroupFallbackJSONResponse(w.context, w.ResponseWriter, data)
	if len(updated) != len(data) && w.Header().Get("Content-Length") != "" {
		if w.Written() {
			updated = data
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(updated)))
		}
	}

	n, err := w.ResponseWriter.Write(updated)
	if err != nil {
		return 0, err
	}
	if n != len(updated) {
		return 0, io.ErrShortWrite
	}
	return len(data), nil
}

func (w *groupFallbackResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func transformGroupFallbackJSONResponse(c *gin.Context, writer gin.ResponseWriter, data []byte) []byte {
	if c == nil || writer == nil || len(data) == 0 {
		return data
	}
	contentType := strings.ToLower(writer.Header().Get("Content-Type"))
	if !strings.Contains(contentType, "json") {
		return data
	}
	updated := service.AppendGroupFallbackResponseMetadata(c, data)
	if bytes.Equal(updated, data) {
		return data
	}
	return updated
}
