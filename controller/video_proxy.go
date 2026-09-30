package controller

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

var videoProxyRequestHeaders = []string{
	"Range",
	"If-Range",
	"If-None-Match",
	"If-Modified-Since",
}

var videoProxyResponseHeaders = []string{
	"Accept-Ranges",
	"Content-Disposition",
	"Content-Encoding",
	"Content-Language",
	"Content-Length",
	"Content-Range",
	"Content-Type",
	"ETag",
	"Last-Modified",
	"Vary",
}

const videoProxyCacheControl = "private, max-age=86400"

// videoProxyError returns a standardized OpenAI-style error response.
func videoProxyError(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
		},
	})
}

func VideoProxy(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error", "task_id is required")
		return
	}

	userID := c.GetInt("id")
	task, exists, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to query task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Task not found")
		return
	}

	if task.Status != model.TaskStatusSuccess {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Task is not completed yet, current status: %s", task.Status))
		return
	}

	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to get channel for task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to retrieve channel information")
		return
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	var videoURL string
	proxy := channel.GetSetting().Proxy
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create proxy client for task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy client")
		return
	}
	client = videoProxyStreamingClient(client)

	method := http.MethodGet
	if c.Request.Method == http.MethodHead {
		method = http.MethodHead
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), method, "", nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create request: %s", err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
		return
	}

	switch channel.Type {
	case constant.ChannelTypeGemini:
		apiKey := task.PrivateData.Key
		if apiKey == "" {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Missing stored API key for Gemini task %s", taskID))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "API key not stored for task")
			return
		}
		videoURL, err = getGeminiVideoURL(channel, task, apiKey)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve Gemini video URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to resolve Gemini video URL")
			return
		}
		req.Header.Set("x-goog-api-key", apiKey)
	case constant.ChannelTypeVertexAi:
		videoURL, err = getVertexVideoURL(channel, task)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve Vertex video URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to resolve Vertex video URL")
			return
		}
	case constant.ChannelTypeOpenAI, constant.ChannelTypeSora:
		videoURL = fmt.Sprintf("%s/v1/videos/%s/content", baseURL, task.GetUpstreamTaskID())
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	default:
		// Video URL is stored in PrivateData.ResultURL (fallback to FailReason for old data)
		videoURL = task.GetResultURL()
	}

	videoURL = strings.TrimSpace(videoURL)
	if videoURL == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video URL is empty for task %s", taskID))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}

	if strings.HasPrefix(videoURL, "data:") {
		if err := writeVideoDataURL(c, videoURL); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to decode video data URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		}
		return
	}

	fetchSetting := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(videoURL, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video URL blocked for task %s: %v", taskID, err))
		videoProxyError(c, http.StatusForbidden, "server_error", fmt.Sprintf("request blocked: %v", err))
		return
	}

	req.URL, err = url.Parse(videoURL)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to parse URL %s: %s", videoURL, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
		return
	}
	copyVideoProxyRequestHeaders(req.Header, c.Request.Header)

	resp, err := client.Do(req)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to fetch video from %s: %s", videoURL, err.Error()))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}
	defer resp.Body.Close()

	if !isVideoProxyPassThroughStatus(resp.StatusCode) {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Upstream returned status %d for %s", resp.StatusCode, videoURL))
		videoProxyError(c, http.StatusBadGateway, "server_error",
			fmt.Sprintf("Upstream service returned status %d", resp.StatusCode))
		return
	}
	if channel.Type == constant.ChannelTypeXai && isClearlyNonVideoContentType(resp.Header.Get("Content-Type")) {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Upstream returned non-video content type %q for %s", resp.Header.Get("Content-Type"), videoURL))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Upstream did not return video content")
		return
	}
	ensureVideoProxyUnsatisfiedContentRange(client, req, resp)

	copyVideoProxyResponseHeaders(c.Writer.Header(), resp.Header)

	c.Writer.Header().Set("Cache-Control", videoProxyCacheControl)
	c.Writer.WriteHeader(resp.StatusCode)
	if method == http.MethodHead {
		return
	}
	if _, err = io.Copy(c.Writer, resp.Body); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to stream video content: %s", err.Error()))
	}
}

func isClearlyNonVideoContentType(raw string) bool {
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		contentType = strings.ToLower(strings.TrimSpace(strings.SplitN(raw, ";", 2)[0]))
	}
	contentType = strings.ToLower(contentType)
	if contentType == "" || strings.HasPrefix(contentType, "video/") || contentType == "application/octet-stream" {
		return false
	}
	return strings.HasPrefix(contentType, "text/") ||
		contentType == "application/json" ||
		contentType == "application/xml" ||
		strings.HasSuffix(contentType, "+json") ||
		strings.HasSuffix(contentType, "+xml")
}

func videoProxyStreamingClient(base *http.Client) *http.Client {
	if base == nil {
		return &http.Client{}
	}
	client := *base
	client.Timeout = 0
	return &client
}

func copyVideoProxyRequestHeaders(destination, source http.Header) {
	for _, key := range videoProxyRequestHeaders {
		if value := source.Get(key); value != "" {
			destination.Set(key, value)
		}
	}
}

func copyVideoProxyResponseHeaders(destination, source http.Header) {
	for _, key := range videoProxyResponseHeaders {
		values := source.Values(key)
		if len(values) == 0 {
			continue
		}
		destination.Del(key)
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

func ensureVideoProxyUnsatisfiedContentRange(client *http.Client, request *http.Request, response *http.Response) {
	if client == nil || request == nil || response == nil ||
		response.StatusCode != http.StatusRequestedRangeNotSatisfiable ||
		response.Header.Get("Content-Range") != "" {
		return
	}

	headRequest := request.Clone(request.Context())
	headRequest.Method = http.MethodHead
	headRequest.Body = nil
	for _, key := range videoProxyRequestHeaders {
		headRequest.Header.Del(key)
	}
	headResponse, err := client.Do(headRequest)
	if err != nil {
		return
	}
	defer headResponse.Body.Close()
	if headResponse.StatusCode >= http.StatusOK && headResponse.StatusCode < http.StatusMultipleChoices && headResponse.ContentLength >= 0 {
		response.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", headResponse.ContentLength))
	}
}

func isVideoProxyPassThroughStatus(status int) bool {
	switch status {
	case http.StatusOK,
		http.StatusPartialContent,
		http.StatusNotModified,
		http.StatusRequestedRangeNotSatisfiable:
		return true
	default:
		return false
	}
}

func writeVideoDataURL(c *gin.Context, dataURL string) error {
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid data url")
	}

	header := parts[0]
	payload := parts[1]
	if !strings.HasPrefix(header, "data:") || !strings.Contains(header, ";base64") {
		return fmt.Errorf("unsupported data url")
	}

	mimeType := strings.TrimPrefix(header, "data:")
	mimeType = strings.TrimSuffix(mimeType, ";base64")
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	videoBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		videoBytes, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return err
		}
	}

	c.Writer.Header().Set("Content-Type", mimeType)
	c.Writer.Header().Set("Cache-Control", videoProxyCacheControl)
	http.ServeContent(c.Writer, c.Request, "video", time.Time{}, bytes.NewReader(videoBytes))
	return nil
}
