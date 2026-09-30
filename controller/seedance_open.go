package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

var seedanceOpenActionPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

var seedanceOpenAllowedActions = map[string]struct{}{
	"ListMediaAssetGroup":         {},
	"CreateVisualValidateSession": {},
	"GetVisualValidateResult":     {},
	"CreateAsset":                 {},
	"GetAsset":                    {},
	"ListAssets":                  {},
	"GetModerationResult":         {},
	"CreateAssetGroup":            {},
}

func PrepareSeedanceOpenRouteRequirement(c *gin.Context) {
	if strings.EqualFold(strings.TrimSpace(c.Param("action")), "CreateAsset") {
		if route := seedanceOpenRouteFromBody(c); service.IsSeedanceAggregateOpenWhiteRoute(route) {
			c.Next()
			return
		}
	}
	if taskErr := service.PrepareSeedanceRouteRequirement(c, ""); taskErr != nil {
		seedanceOpenError(c, taskErr.StatusCode, types.ErrorCodeReadRequestBodyFailed, taskErr.Message)
		c.Abort()
		return
	}
	c.Next()
}

func SeedanceOpenProxy(c *gin.Context) {
	action := strings.TrimSpace(c.Param("action"))
	if action == "" {
		seedanceOpenError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "action is required")
		return
	}
	if !seedanceOpenActionPattern.MatchString(action) {
		seedanceOpenError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "invalid Seedance open action")
		return
	}
	if _, ok := seedanceOpenAllowedActions[action]; !ok {
		seedanceOpenError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, fmt.Sprintf("unsupported Seedance open action %q", action))
		return
	}
	if strings.EqualFold(action, "CreateAsset") {
		if handled := seedanceOpenWhiteCreateAsset(c); handled {
			return
		}
	}

	baseURL := common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl)
	apiKey := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	otherSettings, _ := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	useAKSK := seedanceOpenUseAKSK(otherSettings)
	if strings.TrimSpace(baseURL) == "" {
		seedanceOpenError(c, http.StatusInternalServerError, types.ErrorCodeGetChannelFailed, "selected channel is missing base_url")
		return
	}
	if !useAKSK && strings.TrimSpace(apiKey) == "" {
		seedanceOpenError(c, http.StatusInternalServerError, types.ErrorCodeGetChannelFailed, "selected channel is missing key")
		return
	}

	upstreamBody, err := buildSeedanceOpenUpstreamBody(c)
	if err != nil {
		seedanceOpenError(c, http.StatusBadRequest, types.ErrorCodeBadRequestBody, err.Error())
		return
	}
	upstreamURL := buildSeedanceOpenUpstreamURL(baseURL, action)
	if useAKSK {
		upstreamURL = buildSeedanceOpenOpenAPIURL(otherSettings, action)
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamURL, bytes.NewReader(upstreamBody))
	if err != nil {
		seedanceOpenError(c, http.StatusInternalServerError, types.ErrorCodeDoRequestFailed, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if useAKSK {
		if err := service.SignVolcOpenAPIRequest(
			req,
			upstreamBody,
			strings.TrimSpace(otherSettings.SD2MaterialAccessKeyID),
			strings.TrimSpace(otherSettings.SD2MaterialSecretAccessKey),
			stringDefault(otherSettings.SD2MaterialRegion, "cn-beijing"),
			stringDefault(otherSettings.SD2MaterialService, "ark"),
		); err != nil {
			seedanceOpenError(c, http.StatusInternalServerError, types.ErrorCodeDoRequestFailed, err.Error())
			return
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	settings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	client, err := service.GetHttpClientWithProxy(settings.Proxy)
	if err != nil {
		seedanceOpenError(c, http.StatusInternalServerError, types.ErrorCodeDoRequestFailed, err.Error())
		return
	}
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		seedanceOpenError(c, http.StatusBadGateway, types.ErrorCodeDoRequestFailed, err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		seedanceOpenError(c, http.StatusBadGateway, types.ErrorCodeReadResponseBodyFailed, err.Error())
		return
	}

	forwardSeedanceOpenHeaders(c, resp.Header)
	c.Header("X-New-Api-Upstream-Channel-Id", fmt.Sprint(common.GetContextKeyInt(c, constant.ContextKeyChannelId)))
	c.Header("X-New-Api-Upstream-Channel-Name", common.GetContextKeyString(c, constant.ContextKeyChannelName))
	contentType := resp.Header.Get("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Data(resp.StatusCode, contentType, body)
}

func seedanceOpenWhiteCreateAsset(c *gin.Context) bool {
	var payload map[string]interface{}
	if err := common.UnmarshalBodyReusable(c, &payload); err != nil {
		seedanceOpenError(c, http.StatusBadRequest, types.ErrorCodeBadRequestBody, err.Error())
		return true
	}
	route := seedanceOpenRouteFromPayload(payload)
	if !service.IsSeedanceOpenWhiteRoute(route) {
		if _, hasModels := payload["models"]; hasModels {
			seedanceOpenError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "models requires seedance_route to be all or open_white")
			return true
		}
		return false
	}
	req, err := seedanceOpenWhiteCreateRequestFromPayload(payload)
	if err != nil {
		seedanceOpenError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, err.Error())
		return true
	}
	req.Route = route
	if len(req.Models) > 0 && !service.IsSeedanceAggregateOpenWhiteRoute(route) {
		seedanceOpenError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "models requires seedance_route to be all or open_white")
		return true
	}
	req.UserID = common.GetContextKeyInt(c, constant.ContextKeyUserId)
	req.TokenID = common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	req.GroupName = common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if strings.TrimSpace(req.GroupName) == "" {
		req.GroupName = common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	}
	if service.IsSeedanceAggregateOpenWhiteRoute(route) {
		req.IncludePeers = true
	} else {
		req.ForceChannelID = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	}
	ctx, cancel := seedanceOpenWhiteServiceContext()
	defer cancel()
	if len(req.Models) > 0 {
		resp, err := service.CreateSeedanceOpenWhiteAssets(ctx, req)
		if err != nil {
			seedanceOpenError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, err.Error())
			return true
		}
		if resp.Status == model.SeedanceOpenWhiteStatusFailed {
			c.JSON(http.StatusBadGateway, gin.H{
				"id":      resp.ID,
				"object":  resp.Object,
				"status":  resp.Status,
				"results": resp.Results,
				"error": gin.H{
					"code":    "open_white_batch_all_failed",
					"message": "all requested models failed",
					"results": resp.Results,
				},
			})
			return true
		}
		c.JSON(http.StatusOK, resp)
		return true
	}
	resp, err := service.CreateSeedanceOpenWhiteAsset(ctx, req)
	if err != nil {
		seedanceOpenError(c, http.StatusBadGateway, types.ErrorCodeBadResponse, err.Error())
		return true
	}
	if resp.Status == model.SeedanceOpenWhiteStatusFailed {
		c.JSON(http.StatusBadGateway, gin.H{
			"id":      resp.ID,
			"object":  resp.Object,
			"status":  resp.Status,
			"details": resp.Assets,
			"error": gin.H{
				"code":    "open_white_all_failed",
				"message": "all open-white channels failed",
				"details": resp.Assets,
			},
		})
		return true
	}
	c.JSON(http.StatusOK, resp)
	return true
}

func seedanceOpenRouteFromBody(c *gin.Context) string {
	var payload map[string]interface{}
	if err := common.UnmarshalBodyReusable(c, &payload); err != nil {
		return ""
	}
	return seedanceOpenRouteFromPayload(payload)
}

func seedanceOpenRouteFromPayload(payload map[string]interface{}) string {
	if payload == nil {
		return ""
	}
	for _, key := range []string{"seedance_route", "channel_route", "route"} {
		if value := strings.TrimSpace(fmt.Sprint(payload[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	if metadata, ok := payload["metadata"].(map[string]interface{}); ok {
		for _, key := range []string{"seedance_route", "channel_route", "route"} {
			if value := strings.TrimSpace(fmt.Sprint(metadata[key])); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return ""
}

func seedanceOpenWhiteCreateRequestFromPayload(payload map[string]interface{}) (service.SeedanceOpenWhiteCreateRequest, error) {
	var req service.SeedanceOpenWhiteCreateRequest
	if payload == nil {
		return req, fmt.Errorf("request body is empty")
	}
	req.OpenWhiteID = firstPayloadString(payload, "open_white_id", "id")
	req.Model = firstPayloadString(payload, "model")
	models, err := payloadStringSlice(payload, "models")
	if err != nil {
		return req, err
	}
	if _, hasModels := payload["models"]; hasModels && len(models) == 0 {
		return req, fmt.Errorf("models must contain at least one model")
	}
	req.Models = models
	if len(req.Models) > 0 && strings.TrimSpace(req.Model) != "" {
		return req, fmt.Errorf("model and models cannot be used together")
	}
	if len(req.Models) > 0 && strings.TrimSpace(req.OpenWhiteID) != "" {
		return req, fmt.Errorf("open_white_id cannot be used with models")
	}
	req.SourceURL = firstPayloadString(payload, "source_url", "URL", "Url", "url")
	req.AssetType = firstPayloadString(payload, "AssetType", "asset_type", "assetType")
	req.Name = firstPayloadString(payload, "Name", "name")
	req.MediaKind = strings.ToLower(strings.TrimSpace(firstPayloadString(payload, "media_kind", "mediaKind")))
	if req.MediaKind == "" {
		switch strings.ToLower(req.AssetType) {
		case "video":
			req.MediaKind = "video"
		case "audio":
			req.MediaKind = "audio"
		default:
			req.MediaKind = "image"
		}
	}
	if strings.TrimSpace(req.SourceURL) == "" {
		return req, fmt.Errorf("URL is required")
	}
	return req, nil
}

func payloadStringSlice(payload map[string]interface{}, key string) ([]string, error) {
	raw, exists := payload[key]
	if !exists || raw == nil {
		return nil, nil
	}
	values := make([]string, 0)
	switch typed := raw.(type) {
	case []string:
		values = append(values, typed...)
	case []interface{}:
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be an array of strings", key)
			}
			values = append(values, value)
		}
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	return values, nil
}

func firstPayloadString(payload map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if payload == nil {
			continue
		}
		raw, exists := payload[key]
		if !exists || raw == nil {
			continue
		}
		value := strings.TrimSpace(fmt.Sprint(raw))
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func buildSeedanceOpenUpstreamBody(c *gin.Context) ([]byte, error) {
	var payload map[string]interface{}
	if err := common.UnmarshalBodyReusable(c, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	settings, _ := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	projectName := strings.TrimSpace(settings.SD2MaterialProjectName)

	if nested, ok := nestedSeedanceOpenBody(payload); ok {
		return common.Marshal(withSeedanceOpenProjectName(nested, projectName))
	}

	upstream := make(map[string]interface{}, len(payload))
	for key, value := range payload {
		if isSeedanceOpenControlField(key) {
			continue
		}
		upstream[key] = value
	}
	if projectName != "" {
		upstream["ProjectName"] = projectName
	}
	return common.Marshal(upstream)
}

func withSeedanceOpenProjectName(payload map[string]interface{}, projectName string) map[string]interface{} {
	result := make(map[string]interface{}, len(payload)+1)
	for key, value := range payload {
		result[key] = value
	}
	if projectName = strings.TrimSpace(projectName); projectName != "" {
		result["ProjectName"] = projectName
	}
	return result
}

func nestedSeedanceOpenBody(payload map[string]interface{}) (map[string]interface{}, bool) {
	for _, key := range []string{"body", "request_body", "upstream_body"} {
		raw, ok := payload[key]
		if !ok || raw == nil {
			continue
		}
		nested, ok := raw.(map[string]interface{})
		if !ok {
			return nil, false
		}
		return nested, true
	}
	return nil, false
}

func isSeedanceOpenControlField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "model", "models", "group", "seedance_route", "channel_route", "route", "metadata":
		return true
	default:
		return false
	}
}

func buildSeedanceOpenUpstreamURL(baseURL, action string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/api/v1/doubao/create")
	base = strings.TrimSuffix(base, "/api/v1/doubao/get_result")
	base = strings.TrimSuffix(base, "/api/v1/open")
	base = strings.TrimSuffix(base, "/api/v1/doubao")
	base = strings.TrimSuffix(base, "/api/v1")
	base = strings.TrimRight(base, "/")
	return base + "/api/v1/open/" + action
}

func seedanceOpenUseAKSK(settings dto.ChannelOtherSettings) bool {
	return strings.TrimSpace(settings.SD2MaterialAccessKeyID) != "" &&
		strings.TrimSpace(settings.SD2MaterialSecretAccessKey) != ""
}

func buildSeedanceOpenOpenAPIURL(settings dto.ChannelOtherSettings, action string) string {
	endpoint := stringDefault(settings.SD2MaterialEndpoint, "https://open.volcengineapi.com")
	version := stringDefault(settings.SD2MaterialVersion, "2024-01-01")
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "https://open.volcengineapi.com?Action=" + url.QueryEscape(action) + "&Version=" + url.QueryEscape(version)
	}
	query := u.Query()
	query.Set("Action", action)
	query.Set("Version", version)
	u.RawQuery = query.Encode()
	return u.String()
}

func stringDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func forwardSeedanceOpenHeaders(c *gin.Context, headers http.Header) {
	for key, values := range headers {
		if isHopByHopHeader(key) || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
}

func isHopByHopHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func seedanceOpenError(c *gin.Context, status int, code types.ErrorCode, message string) {
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	if strings.TrimSpace(message) == "" {
		message = string(code)
	}
	newAPIError := types.NewOpenAIError(errors.New(message), code, status)
	c.JSON(status, gin.H{"error": newAPIError.ToOpenAIError()})
}
