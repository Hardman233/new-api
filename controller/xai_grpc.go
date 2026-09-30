package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/xaifile"
	"github.com/gin-gonic/gin"
)

type XAIAuthenticatedContext struct {
	UserID    int    `json:"user_id"`
	TokenID   int    `json:"token_id"`
	TokenName string `json:"token_name,omitempty"`
	Username  string `json:"username,omitempty"`
	UserGroup string `json:"user_group,omitempty"`
}

func XAIGrpcAuthenticate(c *gin.Context) {
	c.JSON(http.StatusOK, XAIAuthenticatedContext{
		UserID:    c.GetInt("id"),
		TokenID:   c.GetInt("token_id"),
		TokenName: c.GetString("token_name"),
		Username:  c.GetString("username"),
		UserGroup: c.GetString(string(constant.ContextKeyUserGroup)),
	})
}

func XAIFilePublicContent(c *gin.Context) {
	fileID := strings.TrimSpace(c.Param("file_id"))
	token := strings.TrimSpace(c.Query("token"))
	file, data, err := xaifile.PublicFile(c.Request.Context(), fileID, token)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"code": "file_not_found", "message": "file not found or public URL expired",
		}})
		return
	}
	contentType := strings.TrimSpace(file.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename := strings.ReplaceAll(file.Filename, `"`, "")
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	c.Header("Content-Length", strconv.FormatInt(int64(len(data)), 10))
	c.Header("Cache-Control", "private, max-age=300")
	c.Data(http.StatusOK, contentType, data)
}
