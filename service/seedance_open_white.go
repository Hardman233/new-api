package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/tidwall/gjson"
)

const (
	seedanceOpenWhiteObject        = "seedance.open_white"
	seedanceOpenWhiteBatchObject   = "seedance.open_white.batch"
	seedanceOpenWhiteStatusSkipped = "skipped"
	defaultSeedanceOpenWhiteModel  = "doubao-seedance-2-0-260128"
)

type SeedanceOpenWhiteCreateRequest struct {
	OpenWhiteID          string   `json:"open_white_id,omitempty"`
	Model                string   `json:"model,omitempty"`
	Models               []string `json:"models,omitempty"`
	GroupName            string   `json:"group_name,omitempty"`
	Route                string   `json:"route,omitempty"`
	ForceChannelID       int      `json:"force_channel_id,omitempty"`
	SourceURL            string   `json:"source_url,omitempty"`
	MediaKind            string   `json:"media_kind,omitempty"`
	AssetType            string   `json:"asset_type,omitempty"`
	Name                 string   `json:"name,omitempty"`
	RequestNodeID        string   `json:"request_node_id,omitempty"`
	UserID               int      `json:"user_id,omitempty"`
	TokenID              int      `json:"token_id,omitempty"`
	IncludePeers         bool     `json:"include_peers,omitempty"`
	SkipIfNoLocalChannel bool     `json:"skip_if_no_local_channel,omitempty"`
}

type SeedanceOpenWhiteResponse struct {
	ID        string                         `json:"id"`
	Object    string                         `json:"object"`
	Status    string                         `json:"status"`
	SourceURL string                         `json:"source_url,omitempty"`
	Assets    []SeedanceOpenWhiteAssetResult `json:"assets"`
}

type SeedanceOpenWhiteBatchResponse struct {
	ID        string                         `json:"id"`
	Object    string                         `json:"object"`
	Status    string                         `json:"status"`
	SourceURL string                         `json:"source_url,omitempty"`
	Assets    []SeedanceOpenWhiteAssetResult `json:"assets"`
	Results   []SeedanceOpenWhiteBatchResult `json:"results"`
}

type SeedanceOpenWhiteBatchResult struct {
	Model        string                         `json:"model"`
	OpenWhiteID  string                         `json:"open_white_id,omitempty"`
	Status       string                         `json:"status"`
	Assets       []SeedanceOpenWhiteAssetResult `json:"assets,omitempty"`
	ErrorCode    string                         `json:"error_code,omitempty"`
	ErrorMessage string                         `json:"error_message,omitempty"`
}

type SeedanceOpenWhiteAssetResult struct {
	NodeID          string `json:"node_id"`
	Region          string `json:"region,omitempty"`
	ChannelID       int    `json:"channel_id"`
	ChannelName     string `json:"channel_name"`
	Route           string `json:"route,omitempty"`
	Status          string `json:"status"`
	UpstreamAssetID string `json:"upstream_asset_id,omitempty"`
	AssetRef        string `json:"asset_ref,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	ErrorMessage    string `json:"error_message,omitempty"`
}

type seedanceOpenWhitePeer struct {
	NodeID  string `json:"node_id"`
	BaseURL string `json:"base_url"`
	Region  string `json:"region"`
}

func IsSeedanceAggregateOpenWhiteRoute(route string) bool {
	switch normalizeSeedanceRouteTag(route) {
	case "all", "open_white":
		return true
	default:
		return false
	}
}

func IsSeedanceOpenWhiteRoute(route string) bool {
	switch normalizeSeedanceRouteTag(route) {
	case "all", "open_white", "zhuque", "xiongda_open_white":
		return true
	default:
		return false
	}
}

func NormalizeSeedanceOpenWhiteID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{"open_white://", "openwhite://"} {
		if strings.HasPrefix(lower, prefix) {
			value = strings.TrimSpace(value[len(prefix):])
			lower = strings.ToLower(value)
			break
		}
	}
	if strings.HasPrefix(lower, "ow_") {
		return value, true
	}
	return "", false
}

func CreateSeedanceOpenWhiteAsset(ctx context.Context, req SeedanceOpenWhiteCreateRequest) (*SeedanceOpenWhiteResponse, error) {
	if len(req.Models) != 0 {
		return nil, fmt.Errorf("models is only supported by the batch open-white operation")
	}
	req.OpenWhiteID = strings.TrimSpace(req.OpenWhiteID)
	if req.OpenWhiteID == "" {
		req.OpenWhiteID = newSeedanceOpenWhiteID()
	}
	req.SourceURL = strings.TrimSpace(req.SourceURL)
	if req.SourceURL == "" {
		return nil, fmt.Errorf("source_url is required")
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		req.Model = defaultSeedanceOpenWhiteModel
	}
	if !IsSeedanceOpenWhiteModelAllowed(req.Model) {
		return nil, fmt.Errorf("model must be one of %s", strings.Join(SeedanceOpenWhiteAllowedModels(), ", "))
	}
	req.GroupName = strings.TrimSpace(req.GroupName)
	if req.GroupName == "" {
		req.GroupName = "default"
	}
	req.Route = normalizeSeedanceRouteTag(req.Route)
	req.MediaKind = strings.ToLower(strings.TrimSpace(req.MediaKind))
	if req.MediaKind == "" {
		req.MediaKind = "image"
	}
	req.AssetType = strings.TrimSpace(req.AssetType)
	if req.AssetType == "" {
		req.AssetType = assetTypeFromMediaKind(req.MediaKind)
	}
	req.RequestNodeID = strings.TrimSpace(req.RequestNodeID)
	if req.RequestNodeID == "" {
		req.RequestNodeID = openWhiteNodeID()
	}

	record, err := model.EnsureSeedanceOpenWhiteRecord(&model.SeedanceOpenWhiteRecord{
		OpenWhiteID:   req.OpenWhiteID,
		UserID:        req.UserID,
		TokenID:       req.TokenID,
		GroupName:     req.GroupName,
		ModelName:     req.Model,
		SourceURLHash: common.Sha256Hex([]byte(req.SourceURL)),
		SourceURL:     req.SourceURL,
		MediaKind:     req.MediaKind,
		AssetType:     req.AssetType,
		Name:          req.Name,
		RequestNodeID: req.RequestNodeID,
		Status:        model.SeedanceOpenWhiteStatusPending,
	})
	if err != nil {
		return nil, err
	}
	// Keep the current model in memory for channel selection while retaining
	// the complete model binding on the shared record.
	bindSeedanceOpenWhiteModel(record, req.Model)

	localResults := createSeedanceOpenWhiteLocalAssets(ctx, *record, req.Route, req.ForceChannelID)
	existingAssets, _ := model.ListSeedanceOpenWhiteAssets(req.OpenWhiteID)
	if len(localResults) == 0 && req.SkipIfNoLocalChannel {
		if len(existingAssets) > 0 {
			return GetSeedanceOpenWhiteResponse(req.OpenWhiteID)
		}
		_ = model.UpdateSeedanceOpenWhiteRecordStatus(req.OpenWhiteID, seedanceOpenWhiteStatusSkipped)
		return &SeedanceOpenWhiteResponse{
			ID:        req.OpenWhiteID,
			Object:    seedanceOpenWhiteObject,
			Status:    seedanceOpenWhiteStatusSkipped,
			SourceURL: req.SourceURL,
			Assets:    []SeedanceOpenWhiteAssetResult{},
		}, nil
	}

	hasResults := len(localResults) > 0 || len(existingAssets) > 0
	if req.IncludePeers {
		for _, peer := range openWhitePeers() {
			peerResults := callSeedanceOpenWhitePeer(ctx, peer, req)
			if len(peerResults) > 0 {
				hasResults = true
			}
			for _, result := range peerResults {
				_ = upsertOpenWhiteResult(req.OpenWhiteID, result, "")
			}
		}
	}
	if !hasResults {
		result := noSeedanceOpenWhiteChannelResult()
		_ = upsertOpenWhiteResult(req.OpenWhiteID, result, "")
	}

	return GetSeedanceOpenWhiteResponse(req.OpenWhiteID)
}

func CreateSeedanceOpenWhiteAssets(ctx context.Context, req SeedanceOpenWhiteCreateRequest) (*SeedanceOpenWhiteBatchResponse, error) {
	if strings.TrimSpace(req.Model) != "" {
		return nil, fmt.Errorf("model and models cannot be used together")
	}
	if strings.TrimSpace(req.OpenWhiteID) != "" {
		return nil, fmt.Errorf("open_white_id cannot be used with models")
	}
	if !IsSeedanceAggregateOpenWhiteRoute(req.Route) {
		return nil, fmt.Errorf("models requires seedance_route to be all or open_white")
	}
	if strings.TrimSpace(req.SourceURL) == "" {
		return nil, fmt.Errorf("source_url is required")
	}
	models, err := normalizeSeedanceOpenWhiteModels(req.Models)
	if err != nil {
		return nil, err
	}

	batch := &SeedanceOpenWhiteBatchResponse{
		ID:        newSeedanceOpenWhiteID(),
		Object:    seedanceOpenWhiteBatchObject,
		Status:    model.SeedanceOpenWhiteStatusPending,
		SourceURL: strings.TrimSpace(req.SourceURL),
		Assets:    []SeedanceOpenWhiteAssetResult{},
		Results:   make([]SeedanceOpenWhiteBatchResult, 0, len(models)),
	}
	for _, modelName := range models {
		childReq := req
		childReq.Models = nil
		childReq.Model = modelName
		childReq.OpenWhiteID = batch.ID

		resp, createErr := CreateSeedanceOpenWhiteAsset(ctx, childReq)
		if createErr != nil {
			batch.Results = append(batch.Results, SeedanceOpenWhiteBatchResult{
				Model:        modelName,
				Status:       model.SeedanceOpenWhiteStatusFailed,
				ErrorCode:    "open_white_create_failed",
				ErrorMessage: createErr.Error(),
			})
			continue
		}
		batch.Results = append(batch.Results, SeedanceOpenWhiteBatchResult{
			Model:       modelName,
			OpenWhiteID: resp.ID,
			Status:      resp.Status,
			Assets:      resp.Assets,
		})
	}
	if response, responseErr := GetSeedanceOpenWhiteResponse(batch.ID); responseErr == nil {
		batch.Status = response.Status
		batch.Assets = response.Assets
	} else {
		batch.Status = summarizeOpenWhiteBatchStatus(batch.Results)
	}
	return batch, nil
}

func bindSeedanceOpenWhiteModel(record *model.SeedanceOpenWhiteRecord, modelName string) {
	if record == nil {
		return
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return
	}
	seen := map[string]struct{}{}
	models := make([]string, 0, 2)
	for _, existing := range strings.Split(record.ModelName, ",") {
		existing = strings.TrimSpace(existing)
		if existing == "" {
			continue
		}
		key := strings.ToLower(existing)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, existing)
	}
	if _, ok := seen[strings.ToLower(modelName)]; !ok {
		models = append(models, modelName)
	}
	if record.Id != 0 {
		_ = model.DB.Model(&model.SeedanceOpenWhiteRecord{}).
			Where("id = ?", record.Id).
			Update("model_name", strings.Join(models, ",")).Error
	}
	record.ModelName = modelName
}

func SeedanceOpenWhiteAllowedModels() []string {
	return []string{
		"doubao-seedance-2-0-260128",
		"doubao-seedance-2-0-fast-260128",
		"doubao-seedance-2-0-mini",
		"doubao-seedance-2-5-260628",
	}
}

func DefaultSeedanceOpenWhiteModel() string {
	return defaultSeedanceOpenWhiteModel
}

func NormalizeSeedanceOpenWhiteModel(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return defaultSeedanceOpenWhiteModel
	}
	return modelName
}

func IsSeedanceOpenWhiteModelAllowed(modelName string) bool {
	modelName = strings.TrimSpace(modelName)
	for _, allowed := range SeedanceOpenWhiteAllowedModels() {
		if strings.EqualFold(modelName, allowed) {
			return true
		}
	}
	return false
}

func normalizeSeedanceOpenWhiteModels(modelNames []string) ([]string, error) {
	if len(modelNames) == 0 {
		return nil, fmt.Errorf("models must contain at least one model")
	}
	allowed := SeedanceOpenWhiteAllowedModels()
	seen := make(map[string]struct{}, len(modelNames))
	normalized := make([]string, 0, len(modelNames))
	for _, modelName := range modelNames {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			return nil, fmt.Errorf("models must not contain an empty model")
		}
		canonical := ""
		for _, candidate := range allowed {
			if strings.EqualFold(modelName, candidate) {
				canonical = candidate
				break
			}
		}
		if canonical == "" {
			return nil, fmt.Errorf("model %q must be one of %s", modelName, strings.Join(allowed, ", "))
		}
		key := strings.ToLower(canonical)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, canonical)
	}
	return normalized, nil
}

func summarizeOpenWhiteBatchStatus(results []SeedanceOpenWhiteBatchResult) string {
	hasActive := false
	hasFailed := false
	hasPending := false
	for _, result := range results {
		switch result.Status {
		case model.SeedanceOpenWhiteStatusActive:
			hasActive = true
		case model.SeedanceOpenWhiteStatusPartialFailed:
			hasActive = true
			hasFailed = true
		case model.SeedanceOpenWhiteStatusFailed, model.SeedanceOpenWhiteStatusTimeout:
			hasFailed = true
		case seedanceOpenWhiteStatusSkipped:
			continue
		default:
			hasPending = true
		}
	}
	if hasActive && hasFailed {
		return model.SeedanceOpenWhiteStatusPartialFailed
	}
	if hasActive && !hasPending {
		return model.SeedanceOpenWhiteStatusActive
	}
	if hasFailed && !hasPending {
		return model.SeedanceOpenWhiteStatusFailed
	}
	return model.SeedanceOpenWhiteStatusPending
}

func GetSeedanceOpenWhiteResponse(openWhiteID string) (*SeedanceOpenWhiteResponse, error) {
	record, err := model.GetSeedanceOpenWhiteRecord(openWhiteID)
	if err != nil {
		return nil, err
	}
	rows, err := model.ListSeedanceOpenWhiteAssets(record.OpenWhiteID)
	if err != nil {
		return nil, err
	}
	assets := make([]SeedanceOpenWhiteAssetResult, 0, len(rows))
	for _, row := range rows {
		assets = append(assets, SeedanceOpenWhiteAssetResult{
			NodeID:          row.NodeID,
			Region:          row.Region,
			ChannelID:       row.ChannelID,
			ChannelName:     row.ChannelName,
			Route:           row.RouteTag,
			Status:          row.Status,
			UpstreamAssetID: row.UpstreamAssetID,
			AssetRef:        row.AssetRef,
			ErrorCode:       row.ErrorCode,
			ErrorMessage:    row.ErrorMessage,
		})
	}
	status := summarizeOpenWhiteStatus(assets)
	if len(assets) == 0 && record.Status == seedanceOpenWhiteStatusSkipped {
		status = seedanceOpenWhiteStatusSkipped
	}
	_ = model.UpdateSeedanceOpenWhiteRecordStatus(record.OpenWhiteID, status)
	return &SeedanceOpenWhiteResponse{
		ID:        record.OpenWhiteID,
		Object:    seedanceOpenWhiteObject,
		Status:    status,
		SourceURL: record.SourceURL,
		Assets:    assets,
	}, nil
}

func ResolveSeedanceOpenWhiteAssetForChannel(openWhiteID string, channelID int) (string, error) {
	openWhiteID, ok := NormalizeSeedanceOpenWhiteID(openWhiteID)
	if !ok {
		return "", fmt.Errorf("invalid open_white_id")
	}
	rows, err := model.ListSeedanceOpenWhiteAssets(openWhiteID)
	if err != nil {
		return "", err
	}
	currentNode := openWhiteNodeID()
	for _, row := range rows {
		if row.ChannelID == channelID &&
			strings.EqualFold(row.NodeID, currentNode) &&
			row.Status == model.SeedanceOpenWhiteStatusActive {
			if ref := openWhiteAssetRef(row); ref != "" {
				return ref, nil
			}
		}
	}
	for _, row := range rows {
		if row.ChannelID == channelID &&
			row.Status == model.SeedanceOpenWhiteStatusActive {
			if ref := openWhiteAssetRef(row); ref != "" {
				return ref, nil
			}
		}
	}
	return "", fmt.Errorf("open_white_id %s has no active asset for channel %d on node %s", openWhiteID, channelID, currentNode)
}

// openWhiteAssetRef returns the asset://<asset_id> reference for a stored row.
// Rows written before the temp-URL fix may hold an expired signed URL in
// asset_ref, so the upstream asset id always wins when it is available.
func openWhiteAssetRef(row model.SeedanceOpenWhiteAsset) string {
	if assetID := strings.TrimSpace(row.UpstreamAssetID); assetID != "" {
		return assetURL(assetID)
	}
	ref := strings.TrimSpace(row.AssetRef)
	if strings.HasPrefix(strings.ToLower(ref), "asset://") {
		return ref
	}
	return ""
}

func createSeedanceOpenWhiteLocalAssets(ctx context.Context, record model.SeedanceOpenWhiteRecord, route string, forceChannelID int) []SeedanceOpenWhiteAssetResult {
	channels, err := listLocalSeedanceOpenWhiteChannels(record.GroupName, record.ModelName, route, forceChannelID)
	if err != nil {
		return []SeedanceOpenWhiteAssetResult{{
			NodeID:       openWhiteNodeID(),
			Region:       openWhiteRegion(),
			ChannelID:    0,
			ChannelName:  "local",
			Status:       model.SeedanceOpenWhiteStatusFailed,
			ErrorCode:    "channel_query_failed",
			ErrorMessage: err.Error(),
		}}
	}
	existingAssets := make(map[string]model.SeedanceOpenWhiteAsset)
	if rows, listErr := model.ListSeedanceOpenWhiteAssets(record.OpenWhiteID); listErr == nil {
		for _, row := range rows {
			key := fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(row.NodeID)), row.ChannelID)
			existingAssets[key] = row
		}
	}
	results := make([]SeedanceOpenWhiteAssetResult, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		route := openWhiteRouteForChannel(channel)
		if route == "" {
			continue
		}
		key := fmt.Sprintf("%s:%d", strings.ToLower(openWhiteNodeID()), channel.Id)
		if existing, ok := existingAssets[key]; ok &&
			existing.Status == model.SeedanceOpenWhiteStatusActive &&
			openWhiteAssetRef(existing) != "" {
			results = append(results, SeedanceOpenWhiteAssetResult{
				NodeID:          existing.NodeID,
				Region:          existing.Region,
				ChannelID:       existing.ChannelID,
				ChannelName:     existing.ChannelName,
				Route:           existing.RouteTag,
				Status:          existing.Status,
				UpstreamAssetID: existing.UpstreamAssetID,
				AssetRef:        openWhiteAssetRef(existing),
				ErrorCode:       existing.ErrorCode,
				ErrorMessage:    existing.ErrorMessage,
			})
			continue
		}
		result := SeedanceOpenWhiteAssetResult{
			NodeID:      openWhiteNodeID(),
			Region:      openWhiteRegion(),
			ChannelID:   channel.Id,
			ChannelName: channel.Name,
			Route:       route,
			Status:      model.SeedanceOpenWhiteStatusPending,
		}
		if ok, code, message := openWhiteChannelSupportsAsset(record, channel, route); !ok {
			result.Status = model.SeedanceOpenWhiteStatusFailed
			result.ErrorCode = code
			result.ErrorMessage = message
			_ = upsertOpenWhiteResult(record.OpenWhiteID, result, "")
			results = append(results, result)
			continue
		}
		release, err := acquireSeedanceOpenWhiteSlot(ctx, fmt.Sprintf("open-white:channel:%d", channel.Id), openWhiteChannelConcurrencyLimit())
		if err != nil {
			result.Status = model.SeedanceOpenWhiteStatusFailed
			result.ErrorCode = openWhiteErrorCode(err)
			result.ErrorMessage = err.Error()
			_ = upsertOpenWhiteResult(record.OpenWhiteID, result, "")
			results = append(results, result)
			continue
		}
		assetID, assetRef, raw, err := func() (string, string, string, error) {
			defer release()
			return createOpenWhiteAssetOnChannel(ctx, channel, record)
		}()
		if err != nil {
			result.Status = model.SeedanceOpenWhiteStatusFailed
			result.ErrorCode = openWhiteErrorCode(err)
			result.ErrorMessage = err.Error()
			_ = upsertOpenWhiteResult(record.OpenWhiteID, result, truncateForDB(raw))
			results = append(results, result)
			continue
		}
		result.Status = model.SeedanceOpenWhiteStatusActive
		result.UpstreamAssetID = assetID
		result.AssetRef = assetRef
		_ = upsertOpenWhiteResult(record.OpenWhiteID, result, truncateForDB(raw))
		results = append(results, result)
	}
	return results
}

func listLocalSeedanceOpenWhiteChannels(group, modelName, route string, forceChannelID int) ([]*model.Channel, error) {
	if forceChannelID > 0 {
		channels, err := listForcedSeedanceOpenWhiteChannels([]int{forceChannelID})
		if err != nil {
			return nil, err
		}
		return filterSeedanceOpenWhiteChannelsByRoute(channels, route)
	}
	if channelIDs := openWhiteForceChannelIDs(); len(channelIDs) > 0 {
		channels, err := listForcedSeedanceOpenWhiteChannels(channelIDs)
		if err != nil {
			return nil, err
		}
		return filterSeedanceOpenWhiteChannelsByModelAndRoute(channels, modelName, route)
	}

	var abilities []model.Ability
	if err := model.DB.Where(&model.Ability{
		Group:   strings.TrimSpace(group),
		Model:   strings.TrimSpace(modelName),
		Enabled: true,
	}).Order("priority desc, weight desc").Find(&abilities).Error; err != nil {
		return nil, err
	}
	seen := map[int]struct{}{}
	channelIDs := make([]int, 0, len(abilities))
	for _, ability := range abilities {
		if _, ok := seen[ability.ChannelId]; ok {
			continue
		}
		seen[ability.ChannelId] = struct{}{}
		channelIDs = append(channelIDs, ability.ChannelId)
	}
	if len(channelIDs) == 0 {
		return nil, nil
	}
	channels, err := model.GetChannelsByIds(channelIDs)
	if err != nil {
		return nil, err
	}
	result := make([]*model.Channel, 0, len(channels))
	for _, channel := range channels {
		if channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		if preferredOpenWhiteRoute(channel.GetOtherSettings().SeedanceRouteTags) == "" {
			continue
		}
		if !seedanceOpenWhiteChannelMatchesRoute(channel, route) {
			continue
		}
		result = append(result, channel)
	}
	return result, nil
}

func filterSeedanceOpenWhiteChannelsByRoute(channels []*model.Channel, route string) ([]*model.Channel, error) {
	result := make([]*model.Channel, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if seedanceOpenWhiteChannelMatchesRoute(channel, route) {
			result = append(result, channel)
		}
	}
	return result, nil
}

func filterSeedanceOpenWhiteChannelsByModelAndRoute(channels []*model.Channel, modelName, route string) ([]*model.Channel, error) {
	result := make([]*model.Channel, 0, len(channels))
	for _, channel := range channels {
		if !seedanceOpenWhiteChannelSupportsModel(channel, modelName) {
			continue
		}
		if seedanceOpenWhiteChannelMatchesRoute(channel, route) {
			result = append(result, channel)
		}
	}
	return result, nil
}

func seedanceOpenWhiteChannelSupportsModel(channel *model.Channel, modelName string) bool {
	if channel == nil || strings.TrimSpace(modelName) == "" {
		return false
	}
	for _, channelModel := range channel.GetModels() {
		channelModel = strings.TrimSpace(channelModel)
		if channelModel == "*" || strings.EqualFold(channelModel, strings.TrimSpace(modelName)) {
			return true
		}
	}
	return false
}

func listForcedSeedanceOpenWhiteChannels(channelIDs []int) ([]*model.Channel, error) {
	channels, err := model.GetChannelsByIds(channelIDs)
	if err != nil {
		return nil, err
	}
	byID := make(map[int]*model.Channel, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		byID[channel.Id] = channel
	}
	result := make([]*model.Channel, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		channel := byID[channelID]
		if channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		result = append(result, channel)
	}
	return result, nil
}

func seedanceOpenWhiteChannelMatchesRoute(channel *model.Channel, route string) bool {
	if channel == nil {
		return false
	}
	route = normalizeSeedanceRouteTag(route)
	preferred := preferredOpenWhiteRoute(channel.GetOtherSettings().SeedanceRouteTags)
	if preferred == "" {
		return false
	}
	switch route {
	case "", "all", "open_white":
		return true
	default:
		return preferred == route
	}
}

func preferredOpenWhiteRoute(tags []string) string {
	seen := map[string]struct{}{}
	for _, tag := range tags {
		normalized := normalizeSeedanceRouteTag(tag)
		if normalized == "" {
			continue
		}
		seen[normalized] = struct{}{}
	}
	for _, route := range []string{"zhuque", "xiongda_open_white", "open_white", "all", "*"} {
		if _, ok := seen[route]; ok {
			if route == "*" || route == "all" {
				return "open_white"
			}
			return route
		}
	}
	return ""
}

func openWhiteRouteForChannel(channel *model.Channel) string {
	if channel == nil {
		return ""
	}
	if route := preferredOpenWhiteRoute(channel.GetOtherSettings().SeedanceRouteTags); route != "" {
		return route
	}
	return "open_white"
}

func createOpenWhiteAssetOnChannel(ctx context.Context, channel *model.Channel, record model.SeedanceOpenWhiteRecord) (assetID, assetRef, raw string, err error) {
	settings := channel.GetOtherSettings()
	if settings.SD2MaterialEnabled {
		return createOpenWhiteAssetViaSD2Material(ctx, channel.Id, record, settings, channel.GetSetting().Proxy)
	}
	return createOpenWhiteAssetViaBearerOpen(ctx, channel, record)
}

func createOpenWhiteAssetViaSD2Material(ctx context.Context, channelID int, record model.SeedanceOpenWhiteRecord, settings dto.ChannelOtherSettings, proxy string) (assetID, assetRef, raw string, err error) {
	client, enabled, err := newSD2MaterialClient(settings, proxy)
	if err != nil {
		return "", "", "", err
	}
	if !enabled {
		return "", "", "", fmt.Errorf("sd2 material is not enabled")
	}
	if strings.TrimSpace(client.assetGroupID) == "" {
		groupID, groupErr := client.ensureAssetGroupID(ctx, channelID)
		if groupErr != nil {
			return "", "", "", groupErr
		}
		client.assetGroupID = groupID
	}
	sourceURLHash := common.Sha256Hex([]byte(record.SourceURL))
	row, err := model.EnsureSD2MaterialAssetRow(channelID, client.assetGroupID, sourceURLHash, record.SourceURL, record.MediaKind)
	if err != nil {
		return "", "", "", err
	}
	if row.Status == model.SD2MaterialStatusActive && strings.TrimSpace(row.AssetID) != "" {
		return row.AssetID, assetURL(row.AssetID), row.LastUpstreamRaw, nil
	}
	assetID = strings.TrimSpace(row.AssetID)
	if assetID == "" {
		assetID, raw, err = client.createAsset(ctx, record.SourceURL, record.MediaKind)
		if err != nil {
			_ = model.MarkSD2MaterialAssetFailed(row.Id, model.SD2MaterialStatusFailed, err.Error(), truncateForDB(raw))
			return "", "", raw, err
		}
		_ = model.MarkSD2MaterialAssetAttempt(row.Id, assetID, model.SD2MaterialStatusPending, truncateForDB(raw))
	}
	status, raw, err := client.waitAssetActive(ctx, assetID)
	if err != nil {
		finalStatus := model.SD2MaterialStatusFailed
		if strings.EqualFold(status, model.SD2MaterialStatusTimeout) {
			finalStatus = model.SD2MaterialStatusTimeout
		}
		_ = model.MarkSD2MaterialAssetFailed(row.Id, finalStatus, err.Error(), truncateForDB(raw))
		return assetID, "", raw, err
	}
	_ = model.MarkSD2MaterialAssetActive(row.Id, assetID, truncateForDB(raw))
	return assetID, assetURL(assetID), raw, nil
}

func createOpenWhiteAssetViaBearerOpen(ctx context.Context, channel *model.Channel, record model.SeedanceOpenWhiteRecord) (assetID, assetRef, raw string, err error) {
	if channel == nil {
		return "", "", "", fmt.Errorf("channel is nil")
	}
	baseURL := ""
	if channel.BaseURL != nil {
		baseURL = strings.TrimSpace(*channel.BaseURL)
	}
	if baseURL == "" {
		return "", "", "", fmt.Errorf("channel base_url is empty")
	}
	key, _, keyErr := channel.GetNextEnabledKey()
	if keyErr != nil {
		return "", "", "", keyErr
	}
	if strings.TrimSpace(key) == "" {
		return "", "", "", fmt.Errorf("channel key is empty")
	}

	createBody := map[string]interface{}{
		"URL":       record.SourceURL,
		"AssetType": record.AssetType,
	}
	if strings.TrimSpace(record.Name) != "" {
		createBody["Name"] = record.Name
	}
	raw, err = doSeedanceBearerOpen(ctx, baseURL, key, "CreateAsset", createBody, channel.GetSetting().Proxy)
	if err != nil {
		return "", "", raw, err
	}
	if err := openAPIError(raw); err != nil {
		return "", "", raw, err
	}
	assetID = firstJSON(raw,
		"Result.AssetId", "Result.AssetID", "Result.asset_id", "Result.Id", "Result.id",
		"Data.AssetId", "Data.AssetID", "Data.asset_id", "Data.Id", "Data.id",
		"AssetId", "AssetID", "asset_id", "Id", "id",
	)
	if strings.TrimSpace(assetID) == "" {
		return "", "", raw, fmt.Errorf("open-white create asset returned empty asset id")
	}

	status, getRaw, err := waitBearerOpenAssetActive(ctx, baseURL, key, assetID, channel.GetSetting().Proxy)
	if strings.TrimSpace(getRaw) != "" {
		raw = getRaw
	}
	if err != nil {
		return assetID, "", raw, err
	}
	if status != model.SD2MaterialStatusActive {
		return assetID, "", raw, fmt.Errorf("open-white asset %s status is %s", assetID, status)
	}
	return assetID, assetURL(assetID), raw, nil
}

func waitBearerOpenAssetActive(ctx context.Context, baseURL, key, assetID, proxy string) (status string, raw string, err error) {
	deadline := time.Now().Add(defaultSD2MaterialPollTimeoutSec * time.Second)
	for {
		raw, err = doSeedanceBearerOpen(ctx, baseURL, key, "GetAsset", map[string]interface{}{"Id": assetID}, proxy)
		if err != nil {
			return status, raw, err
		}
		if apiErr := openAPIError(raw); apiErr != nil {
			return status, raw, apiErr
		}
		status = normalizeMaterialStatus(firstJSON(raw,
			"Result.Status", "Result.status",
			"Data.Status", "Data.status",
			"Status", "status",
		))
		if status == model.SD2MaterialStatusActive {
			return status, raw, nil
		}
		if status == model.SD2MaterialStatusFailed {
			return status, raw, fmt.Errorf("open-white asset %s failed: %s", assetID, firstJSON(raw, "Result.Message", "Data.Message", "Message", "message"))
		}
		if time.Now().After(deadline) {
			return model.SD2MaterialStatusTimeout, raw, fmt.Errorf("open-white asset %s active timeout", assetID)
		}
		select {
		case <-ctx.Done():
			return status, raw, ctx.Err()
		case <-time.After(defaultSD2MaterialPollIntervalSec * time.Second):
		}
	}
}

func doSeedanceBearerOpen(ctx context.Context, baseURL, key, action string, body map[string]interface{}, proxy string) (string, error) {
	bodyBytes, err := common.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildSeedanceBearerOpenURL(baseURL, action), bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	client, err := GetHttpClientWithProxy(proxy)
	if err != nil {
		return "", err
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	raw := string(respBody)
	if readErr != nil {
		return raw, readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return raw, fmt.Errorf("seedance open status %d: %s", resp.StatusCode, raw)
	}
	return raw, nil
}

func buildSeedanceBearerOpenURL(baseURL, action string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/api/v1/doubao/create")
	base = strings.TrimSuffix(base, "/api/v1/doubao/get_result")
	base = strings.TrimSuffix(base, "/api/v1/open")
	base = strings.TrimSuffix(base, "/api/v1/doubao")
	base = strings.TrimSuffix(base, "/api/v1")
	base = strings.TrimRight(base, "/")
	return base + "/api/v1/open/" + url.PathEscape(action)
}

func callSeedanceOpenWhitePeer(ctx context.Context, peer seedanceOpenWhitePeer, req SeedanceOpenWhiteCreateRequest) []SeedanceOpenWhiteAssetResult {
	peer.NodeID = strings.TrimSpace(peer.NodeID)
	if peer.NodeID == "" {
		peer.NodeID = strings.TrimSpace(peer.BaseURL)
	}
	resultOnError := func(code, message string) []SeedanceOpenWhiteAssetResult {
		return []SeedanceOpenWhiteAssetResult{{
			NodeID:       peer.NodeID,
			Region:       peer.Region,
			ChannelID:    0,
			ChannelName:  "peer:" + peer.NodeID,
			Status:       model.SeedanceOpenWhiteStatusFailed,
			ErrorCode:    code,
			ErrorMessage: message,
		}}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(peer.BaseURL), "/")
	if baseURL == "" {
		return resultOnError("peer_config_invalid", "peer base_url is empty")
	}
	key := openWhiteInternalKey()
	if key == "" {
		return resultOnError("peer_internal_key_missing", "OPEN_WHITE_INTERNAL_KEY or INTERNAL_API_KEY is empty")
	}
	release, err := acquireSeedanceOpenWhiteSlot(ctx, "open-white:peer:"+peer.NodeID, openWhitePeerConcurrencyLimit())
	if err != nil {
		return resultOnError(openWhiteErrorCode(err), err.Error())
	}
	defer release()
	peerReq := req
	peerReq.IncludePeers = false
	peerReq.SkipIfNoLocalChannel = true
	peerReq.RequestNodeID = openWhiteNodeID()
	body, err := common.Marshal(peerReq)
	if err != nil {
		return resultOnError("peer_request_marshal_failed", err.Error())
	}
	peerCtx, cancel := context.WithTimeout(ctx, time.Duration(common.GetEnvOrDefault("OPEN_WHITE_PEER_TIMEOUT_SEC", 120))*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(peerCtx, http.MethodPost, baseURL+"/api/internal/seedance/open_white/create_asset", bytes.NewReader(body))
	if err != nil {
		return resultOnError("peer_request_failed", err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Internal-Api-Key", key)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return resultOnError("peer_unavailable", err.Error())
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resultOnError("peer_read_failed", readErr.Error())
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resultOnError("peer_http_error", fmt.Sprintf("status=%d body=%s", resp.StatusCode, string(respBody)))
	}
	var peerResp SeedanceOpenWhiteResponse
	if err := common.Unmarshal(respBody, &peerResp); err != nil {
		return resultOnError("peer_unmarshal_failed", err.Error())
	}
	if peerResp.Status == seedanceOpenWhiteStatusSkipped && len(peerResp.Assets) == 0 {
		return nil
	}
	if len(peerResp.Assets) == 0 {
		return resultOnError("peer_empty_result", "peer returned no assets")
	}
	return peerResp.Assets
}

func noSeedanceOpenWhiteChannelResult() SeedanceOpenWhiteAssetResult {
	return SeedanceOpenWhiteAssetResult{
		NodeID:       openWhiteNodeID(),
		Region:       openWhiteRegion(),
		ChannelID:    0,
		ChannelName:  "local",
		Status:       model.SeedanceOpenWhiteStatusFailed,
		ErrorCode:    "no_open_white_channel",
		ErrorMessage: "no Seedance open-white channel matched the requested model and route on any configured node",
	}
}

func upsertOpenWhiteResult(openWhiteID string, result SeedanceOpenWhiteAssetResult, raw string) error {
	return model.UpsertSeedanceOpenWhiteAsset(&model.SeedanceOpenWhiteAsset{
		OpenWhiteID:     strings.TrimSpace(openWhiteID),
		NodeID:          stringDefault(result.NodeID, openWhiteNodeID()),
		Region:          result.Region,
		ChannelID:       result.ChannelID,
		ChannelName:     result.ChannelName,
		RouteTag:        result.Route,
		UpstreamAssetID: result.UpstreamAssetID,
		AssetRef:        result.AssetRef,
		Status:          result.Status,
		ErrorCode:       result.ErrorCode,
		ErrorMessage:    result.ErrorMessage,
		LastUpstreamRaw: raw,
	})
}

func summarizeOpenWhiteStatus(assets []SeedanceOpenWhiteAssetResult) string {
	active := 0
	failed := 0
	for _, asset := range assets {
		switch asset.Status {
		case model.SeedanceOpenWhiteStatusActive:
			active++
		case model.SeedanceOpenWhiteStatusFailed, model.SeedanceOpenWhiteStatusTimeout:
			failed++
		}
	}
	if active > 0 && failed > 0 {
		return model.SeedanceOpenWhiteStatusPartialFailed
	}
	if active > 0 {
		return model.SeedanceOpenWhiteStatusActive
	}
	if failed > 0 {
		return model.SeedanceOpenWhiteStatusFailed
	}
	return model.SeedanceOpenWhiteStatusPending
}

func newSeedanceOpenWhiteID() string {
	return "ow_" + time.Now().UTC().Format("20060102T150405") + "_" + strings.ToLower(common.GetRandomString(8))
}

func openWhiteNodeID() string {
	if value := strings.TrimSpace(os.Getenv("OPEN_WHITE_NODE_ID")); value != "" {
		return value
	}
	if value := strings.TrimSpace(common.NodeName); value != "" {
		return value
	}
	return "local"
}

func openWhiteRegion() string {
	if value := strings.TrimSpace(os.Getenv("OPEN_WHITE_REGION")); value != "" {
		return value
	}
	return openWhiteNodeID()
}

func openWhiteInternalKey() string {
	if value := strings.TrimSpace(os.Getenv("OPEN_WHITE_INTERNAL_KEY")); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv("INTERNAL_API_KEY"))
}

func openWhitePeers() []seedanceOpenWhitePeer {
	raw := strings.TrimSpace(os.Getenv("OPEN_WHITE_PEERS"))
	if raw == "" {
		return nil
	}
	var peers []seedanceOpenWhitePeer
	if err := common.Unmarshal([]byte(raw), &peers); err != nil {
		common.SysLog("invalid OPEN_WHITE_PEERS: " + err.Error())
		return nil
	}
	current := openWhiteNodeID()
	out := make([]seedanceOpenWhitePeer, 0, len(peers))
	for _, peer := range peers {
		peer.NodeID = strings.TrimSpace(peer.NodeID)
		peer.BaseURL = strings.TrimRight(strings.TrimSpace(peer.BaseURL), "/")
		if peer.BaseURL == "" || strings.EqualFold(peer.NodeID, current) {
			continue
		}
		out = append(out, peer)
	}
	return out
}

func openWhiteForceChannelIDs() []int {
	raw := strings.TrimSpace(os.Getenv("OPEN_WHITE_FORCE_CHANNEL_IDS"))
	if raw == "" {
		return nil
	}
	seen := map[int]struct{}{}
	ids := make([]int, 0)
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', ' ', '\t', '\n', '\r':
			return true
		default:
			return false
		}
	}) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.Atoi(part)
		if err != nil || id <= 0 {
			common.SysLog("invalid OPEN_WHITE_FORCE_CHANNEL_IDS item: " + part)
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func assetTypeFromMediaKind(mediaKind string) string {
	switch strings.ToLower(strings.TrimSpace(mediaKind)) {
	case "video":
		return "Video"
	case "audio":
		return "Audio"
	default:
		return "Image"
	}
}

func openWhiteChannelSupportsAsset(record model.SeedanceOpenWhiteRecord, channel *model.Channel, route string) (bool, string, string) {
	if channel == nil {
		return false, "channel_invalid", "channel is nil"
	}
	if _, ok := openWhiteAssetKind(record); !ok {
		return false, "unsupported_asset_type", fmt.Sprintf("unsupported AssetType %q for channel #%d %s; supported types are Image, Video, Audio", record.AssetType, channel.Id, channel.Name)
	}
	return true, "", ""
}

func openWhiteAssetKind(record model.SeedanceOpenWhiteRecord) (string, bool) {
	assetType := strings.TrimSpace(record.AssetType)
	if assetType != "" {
		switch normalizeSeedanceRouteTag(assetType) {
		case "image", "img", "picture", "photo":
			return "image", true
		case "video":
			return "video", true
		case "audio", "sound":
			return "audio", true
		default:
			return "", false
		}
	}
	switch normalizeSeedanceRouteTag(record.MediaKind) {
	case "", "image", "img", "picture", "photo":
		return "image", true
	case "video":
		return "video", true
	case "audio", "sound":
		return "audio", true
	default:
		return "", false
	}
}

func seedanceOpenWhiteTagsSupportAudio(tags []string) bool {
	for _, tag := range tags {
		switch normalizeSeedanceRouteTag(tag) {
		case "*", "all", "open_white", "zhuque", "xiongda_open_white", "audio", "reference_audio", "audio_open_white", "open_white_audio":
			return true
		}
	}
	return false
}

func openWhiteErrorCode(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "queue"):
		return "open_white_queue_timeout"
	case strings.Contains(msg, "asset type") && (strings.Contains(msg, "not configured") || strings.Contains(msg, "unsupported")):
		return "unsupported_asset_type"
	case strings.Contains(msg, "timeout"):
		return "upstream_timeout"
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "401"):
		return "upstream_unauthorized"
	case strings.Contains(msg, "sensitive") || strings.Contains(msg, "privacy"):
		return "upstream_sensitive_rejected"
	case strings.Contains(msg, "empty asset"):
		return "upstream_empty_asset"
	default:
		return "upstream_error"
	}
}

func gjsonString(raw string, paths ...string) string {
	for _, path := range paths {
		value := strings.TrimSpace(gjson.Get(raw, path).String())
		if value != "" {
			return value
		}
	}
	return ""
}
