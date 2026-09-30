package service

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const (
	ginKeySeedanceRouteRequirement = "seedance_route_requirement"
	ginKeySeedanceRouteAdminInfo   = "seedance_route_admin_info"
)

type SeedanceRouteRequirement struct {
	Route        string            `json:"route"`
	RequiredTags []string          `json:"required_tags"`
	Signals      map[string]string `json:"signals,omitempty"`
}

type seedanceRouteSignals struct {
	firstFrame      bool
	lastFrame       bool
	referenceImage  bool
	referenceVideo  bool
	referenceAudio  bool
	emptyRoleImages int
}

// PrepareSeedanceRouteRequirement inspects a video request before channel
// selection. The result is used as a per-request channel capability filter.
func PrepareSeedanceRouteRequirement(c *gin.Context, modelName string) *dto.TaskError {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return nil
	}
	if !isSeedanceRouteInspectablePath(c.Request.URL.Path) && !isSeedanceModelName(modelName) {
		return nil
	}

	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			return TaskErrorWrapperLocal(err, "read_request_body_failed", http.StatusRequestEntityTooLarge)
		}
		// Let the normal task validator return the exact invalid-request error.
		return nil
	}
	req.FillPromptFromContent()
	if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		req.Images = []string{req.Image}
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = req.Model
	}
	if !isSeedanceModelName(modelName) {
		return nil
	}

	route := ClassifySeedanceRoute(req)
	if len(route.RequiredTags) == 0 {
		return nil
	}
	c.Set(ginKeySeedanceRouteRequirement, route)
	c.Set(ginKeySeedanceRouteAdminInfo, map[string]interface{}{
		"route":         route.Route,
		"required_tags": route.RequiredTags,
		"signals":       route.Signals,
	})
	return nil
}

func isSeedanceRouteInspectablePath(path string) bool {
	return strings.HasPrefix(path, "/v1/videos") ||
		strings.HasPrefix(path, "/v1/seedance/open") ||
		strings.HasPrefix(path, "/api/v1/open")
}

func ClassifySeedanceRoute(req relaycommon.TaskSubmitReq) SeedanceRouteRequirement {
	if explicit := seedanceExplicitRoute(req); explicit != "" {
		routeName := normalizeSeedanceRouteTag(explicit)
		tags := expandSeedanceRouteTag(routeName)
		tags = normalizeUniqueSeedanceTags(tags)
		return SeedanceRouteRequirement{
			Route:        routeName,
			RequiredTags: tags,
			Signals: map[string]string{
				"explicit_route": explicit,
				"route_source":   "request",
			},
		}
	}

	tags := map[string]struct{}{}
	signals := map[string]string{}
	addTag := func(tag string) {
		tag = normalizeSeedanceRouteTag(tag)
		if tag != "" {
			tags[tag] = struct{}{}
		}
	}
	addSignal := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			signals[key] = value
		}
	}

	media := seedanceRouteSignals{}
	switch len(req.Images) {
	case 0:
	case 1:
		media.firstFrame = true
		addSignal("images", "single_first_frame")
	default:
		media.referenceImage = true
		addSignal("images", "multiple_reference_images")
	}

	scanSeedanceContent(req.Content, &media)
	if req.Metadata != nil {
		scanSeedanceContent(req.Metadata["content"], &media)
	}
	if media.emptyRoleImages == 1 && !media.firstFrame && !media.lastFrame && !media.referenceImage {
		media.firstFrame = true
	}
	if media.emptyRoleImages > 1 {
		media.referenceImage = true
	}

	if media.lastFrame {
		addTag("first_last_frame")
		addSignal("frame_mode", "first_last_frame")
	} else if media.firstFrame {
		addTag("first_frame")
		addSignal("frame_mode", "first_frame")
	}
	if media.referenceImage {
		addTag("reference_image")
		addSignal("reference_image", "true")
	}
	if media.referenceVideo {
		addTag("reference_video")
		addSignal("reference_video", "true")
	}
	if media.referenceAudio {
		addTag("reference_audio")
		addSignal("reference_audio", "true")
	}
	if !media.firstFrame && !media.lastFrame && !media.referenceImage && !media.referenceVideo && !media.referenceAudio && media.emptyRoleImages == 0 {
		addTag("text_to_video")
		addSignal("media_mode", "text_to_video")
	}

	if seedanceResolution(req) == "1080p" {
		addTag("1080p")
		addSignal("resolution", "1080p")
	}
	if seedanceTruthy(req.GenerateAudio) || seedanceTruthy(metadataValue(req.Metadata, "generate_audio")) {
		addTag("generate_audio")
		addSignal("generate_audio", "true")
	}
	if seedanceTruthy(req.ReturnLastFrame) || seedanceTruthy(metadataValue(req.Metadata, "return_last_frame")) {
		addTag("return_last_frame")
		addSignal("return_last_frame", "true")
	}
	if seedanceHasOfficialAdvancedParams(req) {
		addTag("official_params")
		addSignal("official_params", "true")
	}

	requiredTags := make([]string, 0, len(tags))
	for tag := range tags {
		requiredTags = append(requiredTags, tag)
	}
	sort.Strings(requiredTags)

	return SeedanceRouteRequirement{
		Route:        seedancePrimaryRoute(requiredTags),
		RequiredTags: requiredTags,
		Signals:      signals,
	}
}

func getSeedanceRouteStrictChannelFilter(c *gin.Context) (model.ChannelFilter, bool) {
	route, ok := GetSeedanceRouteRequirement(c)
	if !ok || len(route.RequiredTags) == 0 {
		return nil, false
	}
	return func(channel *model.Channel) bool {
		if channel == nil {
			return false
		}
		settings := channel.GetOtherSettings()
		return seedanceChannelSupportsTags(settings.SeedanceRouteTags, route.RequiredTags)
	}, true
}

func GetSeedanceRouteRequirement(c *gin.Context) (SeedanceRouteRequirement, bool) {
	if c == nil {
		return SeedanceRouteRequirement{}, false
	}
	raw, ok := c.Get(ginKeySeedanceRouteRequirement)
	if !ok {
		return SeedanceRouteRequirement{}, false
	}
	route, ok := raw.(SeedanceRouteRequirement)
	return route, ok
}

func AppendSeedanceRouteAdminInfo(c *gin.Context, adminInfo map[string]interface{}) {
	if c == nil || adminInfo == nil {
		return
	}
	if raw, ok := c.Get(ginKeySeedanceRouteAdminInfo); ok && raw != nil {
		adminInfo["seedance_route"] = raw
	}
}

func getRandomSatisfiedChannelForRequest(c *gin.Context, group string, modelName string, retry int, requestFilter model.ChannelFilter) (*model.Channel, error) {
	routeFilter, hasRouteFilter := getSeedanceRouteStrictChannelFilter(c)
	concurrencyState := newChannelConcurrencyFilterState()
	filter := combineChannelFilters(routeFilter, requestFilter, concurrencyState.Filter)
	if hasRouteFilter || filter != nil {
		channel, err := model.GetRandomSatisfiedChannelWithFilter(group, modelName, retry, filter)
		if err != nil {
			return nil, err
		}
		if channel != nil {
			if route, exists := GetSeedanceRouteRequirement(c); exists {
				c.Set(ginKeySeedanceRouteAdminInfo, map[string]interface{}{
					"route":         route.Route,
					"required_tags": route.RequiredTags,
					"signals":       route.Signals,
					"matched":       true,
					"channel_id":    channel.Id,
				})
			}
			return channel, nil
		}
		if hasRouteFilter {
			if route, exists := GetSeedanceRouteRequirement(c); exists && seedanceRouteIsExplicit(route) {
				c.Set(ginKeySeedanceRouteAdminInfo, map[string]interface{}{
					"route":         route.Route,
					"required_tags": route.RequiredTags,
					"signals":       route.Signals,
					"matched":       false,
				})
				return nil, fmt.Errorf("no Seedance channel matched explicit seedance_route %q in group %s for model %s; configure channel settings.seedance_route_tags", route.Route, group, modelName)
			}
		}
		if err := concurrencyState.SaturatedError(group, modelName); err != nil {
			return nil, err
		}
		if route, exists := GetSeedanceRouteRequirement(c); exists && seedanceRouteIsExplicit(route) {
			c.Set(ginKeySeedanceRouteAdminInfo, map[string]interface{}{
				"route":         route.Route,
				"required_tags": route.RequiredTags,
				"signals":       route.Signals,
				"matched":       false,
			})
			return nil, fmt.Errorf("no Seedance channel matched explicit seedance_route %q in group %s for model %s; configure channel settings.seedance_route_tags", route.Route, group, modelName)
		}
		if requestFilter != nil {
			// Image capability filters are correctness constraints. Never fall
			// back to an unfiltered channel after an edit/generation mismatch.
			return nil, nil
		}
	}
	return model.GetRandomSatisfiedChannel(group, modelName, retry)
}

func combineChannelFilters(filters ...model.ChannelFilter) model.ChannelFilter {
	nonNil := make([]model.ChannelFilter, 0, len(filters))
	for _, filter := range filters {
		if filter != nil {
			nonNil = append(nonNil, filter)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	return func(channel *model.Channel) bool {
		for _, filter := range nonNil {
			if !filter(channel) {
				return false
			}
		}
		return true
	}
}

func seedanceRouteIsExplicit(route SeedanceRouteRequirement) bool {
	if route.Signals == nil {
		return false
	}
	return route.Signals["route_source"] == "request"
}

func seedanceChannelSupportsTags(channelTags []string, requiredTags []string) bool {
	if len(channelTags) == 0 || len(requiredTags) == 0 {
		return false
	}
	supported := map[string]struct{}{}
	for _, tag := range channelTags {
		normalized := normalizeSeedanceRouteTag(tag)
		if normalized == "" {
			continue
		}
		if normalized == "*" || normalized == "all" || normalized == "open_white" {
			return true
		}
		for _, expanded := range expandSeedanceRouteTag(normalized) {
			supported[expanded] = struct{}{}
		}
	}
	for _, required := range requiredTags {
		required = normalizeSeedanceRouteTag(required)
		if required == "" {
			continue
		}
		requiredExpanded := expandSeedanceRouteTag(required)
		for _, expanded := range requiredExpanded {
			if _, ok := supported[expanded]; !ok {
				return false
			}
		}
	}
	return true
}

func expandSeedanceRouteTag(tag string) []string {
	switch tag {
	case "basic":
		return []string{"basic", "text_to_video", "first_frame"}
	case "reference":
		return []string{"reference", "reference_image", "reference_video", "reference_audio"}
	case "advanced":
		return []string{"advanced", "first_last_frame", "reference_image", "reference_video", "reference_audio", "1080p", "generate_audio", "return_last_frame", "official_params"}
	default:
		return []string{tag}
	}
}

func seedanceExplicitRoute(req relaycommon.TaskSubmitReq) string {
	if s := strings.TrimSpace(req.SeedanceRoute); s != "" {
		return s
	}
	if s := strings.TrimSpace(req.ChannelRoute); s != "" {
		return s
	}
	if s := strings.TrimSpace(req.Route); s != "" {
		return s
	}
	for _, value := range []any{
		metadataValue(req.Metadata, "seedance_route"),
		metadataValue(req.Metadata, "channel_route"),
		metadataValue(req.Metadata, "route"),
	} {
		if s := strings.TrimSpace(fmt.Sprint(value)); s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

func normalizeUniqueSeedanceTags(tags []string) []string {
	result := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, tag := range tags {
		tag = normalizeSeedanceRouteTag(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}

func scanSeedanceContent(raw any, signals *seedanceRouteSignals) {
	switch items := raw.(type) {
	case []map[string]interface{}:
		for _, item := range items {
			scanSeedanceContentItem(item, signals)
		}
	case []interface{}:
		for _, rawItem := range items {
			if item, ok := rawItem.(map[string]interface{}); ok {
				scanSeedanceContentItem(item, signals)
			}
		}
	}
}

func scanSeedanceContentItem(item map[string]interface{}, signals *seedanceRouteSignals) {
	if item == nil || signals == nil {
		return
	}
	itemType := normalizeSeedanceRouteTag(fmt.Sprint(item["type"]))
	if itemType == "" || itemType == "<nil>" {
		itemType = seedanceContentTypeFromMap(item)
	}
	role := normalizeSeedanceRouteTag(fmt.Sprint(item["role"]))
	if role == "<nil>" {
		role = ""
	}

	switch itemType {
	case "image_url":
		switch role {
		case "first_frame":
			signals.firstFrame = true
		case "last_frame", "first_last_frame":
			signals.lastFrame = true
			signals.firstFrame = true
		case "reference_image":
			signals.referenceImage = true
		default:
			signals.emptyRoleImages++
		}
	case "video_url":
		signals.referenceVideo = true
	case "audio_url":
		signals.referenceAudio = true
	}
}

func seedanceContentTypeFromMap(item map[string]interface{}) string {
	if _, ok := item["image_url"]; ok {
		return "image_url"
	}
	if _, ok := item["video_url"]; ok {
		return "video_url"
	}
	if _, ok := item["audio_url"]; ok {
		return "audio_url"
	}
	if text, ok := item["text"].(string); ok && strings.TrimSpace(text) != "" {
		return "text"
	}
	return ""
}

func seedanceResolution(req relaycommon.TaskSubmitReq) string {
	resolution := normalizeSeedanceRouteTag(req.Resolution)
	if resolution == "" {
		resolution = normalizeSeedanceRouteTag(fmt.Sprint(metadataValue(req.Metadata, "resolution")))
	}
	if resolution == "<nil>" {
		return ""
	}
	return resolution
}

func seedanceHasOfficialAdvancedParams(req relaycommon.TaskSubmitReq) bool {
	if strings.TrimSpace(req.CallbackURL) != "" ||
		strings.TrimSpace(req.ServiceTier) != "" ||
		req.ExecutionExpiresAfter != nil ||
		req.Draft != nil ||
		len(req.Tools) > 0 ||
		req.Frames != nil ||
		req.Seed != nil ||
		req.CameraFixed != nil ||
		req.Watermark != nil {
		return true
	}
	for _, key := range []string{
		"callback_url",
		"service_tier",
		"execution_expires_after",
		"draft",
		"tools",
		"frames",
		"seed",
		"camera_fixed",
		"watermark",
	} {
		if !seedanceMetadataEmpty(metadataValue(req.Metadata, key)) {
			return true
		}
	}
	return false
}

func seedancePrimaryRoute(tags []string) string {
	has := func(tag string) bool {
		for _, item := range tags {
			if item == tag {
				return true
			}
		}
		return false
	}
	switch {
	case has("reference_audio"):
		return "reference_audio"
	case has("reference_video"):
		return "reference_video"
	case has("first_last_frame"):
		return "first_last_frame"
	case has("reference_image"):
		return "reference_image"
	case has("first_frame"):
		return "first_frame"
	case has("text_to_video"):
		return "text_to_video"
	default:
		return "custom"
	}
}

func normalizeSeedanceRouteTag(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "t2v", "text2video", "text_to_video":
		return "text_to_video"
	case "i2v", "image2video", "image_to_video":
		return "first_frame"
	case "first_last", "first_and_last", "last_frame":
		return "first_last_frame"
	case "ref", "reference":
		return "reference"
	case "ref_image":
		return "reference_image"
	case "ref_video":
		return "reference_video"
	case "ref_audio":
		return "reference_audio"
	case "1080", "1080p":
		return "1080p"
	case "朱雀", "zhuque":
		return "zhuque"
	case "xiongda", "xiongda_openwhite", "xiongda_open_white", "xiongda_whitelist":
		return "xiongda_open_white"
	case "开白", "白名单", "openwhite", "open_white":
		return "open_white"
	case "open_whitelist", "whitelist", "zhuque_open_white", "zhuque_whitelist":
		return "open_white"
	default:
		return value
	}
}

func metadataValue(metadata map[string]interface{}, key string) any {
	if metadata == nil {
		return nil
	}
	return metadata[key]
}

func seedanceMetadataEmpty(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []interface{}:
		return len(v) == 0
	case []map[string]interface{}:
		return len(v) == 0
	default:
		return false
	}
}

func seedanceTruthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "y", "on":
			return true
		default:
			return false
		}
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return false
	}
}

func isSeedanceModelName(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "seedance")
}
