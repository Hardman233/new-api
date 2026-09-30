package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

const (
	defaultSD2MaterialEndpoint          = "https://open.volcengineapi.com"
	defaultSD2MaterialRegion            = "cn-beijing"
	defaultSD2MaterialService           = "ark"
	defaultSD2MaterialVersion           = "2024-01-01"
	defaultSD2MaterialCreateGroupAction = "CreateAssetGroup"
	defaultSD2MaterialCreateAction      = "CreateAsset"
	defaultSD2MaterialGetAction         = "GetAsset"
	defaultSD2MaterialPollTimeoutSec    = 60
	defaultSD2MaterialPollIntervalSec   = 2
	defaultSD2MaterialImageAssetType    = "Image"
	defaultSD2MaterialVideoAssetType    = "Video"
	defaultSD2MaterialAudioAssetType    = "Audio"
)

type sd2MaterialClient struct {
	endpoint          string
	region            string
	serviceName       string
	version           string
	createAction      string
	getAction         string
	accessKeyID       string
	secretAccessKey   string
	assetGroupID      string
	assetGroupName    string
	projectName       string
	createGroupAction string
	pollTimeout       time.Duration
	pollInterval      time.Duration
	imageAssetType    string
	videoAssetType    string
	audioAssetType    string
	createGroupBody   string
	createBody        string
	getBody           string
	httpClient        *http.Client
}

func EnsureSD2MaterialAsset(ctx context.Context, channelID int, sourceURL string, mediaKind string, settings dto.ChannelOtherSettings, proxy string) (string, error) {
	sourceURL = strings.TrimSpace(sourceURL)
	if !ShouldUseSD2MaterialAsset(sourceURL) {
		return sourceURL, nil
	}
	client, enabled, err := newSD2MaterialClient(settings, proxy)
	if err != nil {
		return "", err
	}
	if !enabled {
		return sourceURL, nil
	}
	if strings.TrimSpace(client.assetGroupID) == "" {
		groupID, err := client.ensureAssetGroupID(ctx, channelID)
		if err != nil {
			return "", err
		}
		client.assetGroupID = groupID
	}

	sourceURLHash := common.Sha256Hex([]byte(sourceURL))
	row, err := model.EnsureSD2MaterialAssetRow(channelID, client.assetGroupID, sourceURLHash, sourceURL, mediaKind)
	if err != nil {
		return "", err
	}
	if row.Status == model.SD2MaterialStatusActive && strings.TrimSpace(row.AssetID) != "" {
		return assetURL(row.AssetID), nil
	}

	assetID := strings.TrimSpace(row.AssetID)
	if assetID == "" {
		var raw string
		assetID, raw, err = client.createAsset(ctx, sourceURL, mediaKind)
		if err != nil {
			_ = model.MarkSD2MaterialAssetFailed(row.Id, model.SD2MaterialStatusFailed, err.Error(), truncateForDB(raw))
			return "", err
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
		return "", err
	}
	_ = model.MarkSD2MaterialAssetActive(row.Id, assetID, truncateForDB(raw))
	return assetURL(assetID), nil
}

func ShouldUseSD2MaterialAsset(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	lower := strings.ToLower(rawURL)
	if strings.HasPrefix(lower, "asset://") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "base64:") {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}

func newSD2MaterialClient(settings dto.ChannelOtherSettings, proxy string) (*sd2MaterialClient, bool, error) {
	if !settings.SD2MaterialEnabled {
		return nil, false, nil
	}
	accessKeyID := strings.TrimSpace(settings.SD2MaterialAccessKeyID)
	secretAccessKey := strings.TrimSpace(settings.SD2MaterialSecretAccessKey)
	assetGroupID := strings.TrimSpace(settings.SD2MaterialAssetGroupID)
	assetGroupName := stringDefault(settings.SD2MaterialAssetGroupName, "oc-studio-sd2")
	projectName := strings.TrimSpace(settings.SD2MaterialProjectName)
	if accessKeyID == "" || secretAccessKey == "" {
		return nil, true, fmt.Errorf("sd2 material is enabled but access_key_id or secret_access_key is empty")
	}
	httpClient, err := GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, true, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	pollTimeout := time.Duration(intDefault(settings.SD2MaterialPollTimeoutSec, defaultSD2MaterialPollTimeoutSec)) * time.Second
	pollInterval := time.Duration(intDefault(settings.SD2MaterialPollIntervalSec, defaultSD2MaterialPollIntervalSec)) * time.Second
	if pollInterval <= 0 {
		pollInterval = defaultSD2MaterialPollIntervalSec * time.Second
	}
	if pollTimeout <= 0 {
		pollTimeout = defaultSD2MaterialPollTimeoutSec * time.Second
	}

	return &sd2MaterialClient{
		endpoint:          stringDefault(settings.SD2MaterialEndpoint, defaultSD2MaterialEndpoint),
		region:            stringDefault(settings.SD2MaterialRegion, defaultSD2MaterialRegion),
		serviceName:       stringDefault(settings.SD2MaterialService, defaultSD2MaterialService),
		version:           stringDefault(settings.SD2MaterialVersion, defaultSD2MaterialVersion),
		createGroupAction: stringDefault(settings.SD2MaterialCreateGroupAction, defaultSD2MaterialCreateGroupAction),
		createAction:      stringDefault(settings.SD2MaterialCreateAction, defaultSD2MaterialCreateAction),
		getAction:         stringDefault(settings.SD2MaterialGetAction, defaultSD2MaterialGetAction),
		accessKeyID:       accessKeyID,
		secretAccessKey:   secretAccessKey,
		assetGroupID:      assetGroupID,
		assetGroupName:    assetGroupName,
		projectName:       projectName,
		pollTimeout:       pollTimeout,
		pollInterval:      pollInterval,
		imageAssetType:    stringDefault(settings.SD2MaterialImageAssetType, defaultSD2MaterialImageAssetType),
		videoAssetType:    stringDefault(settings.SD2MaterialVideoAssetType, defaultSD2MaterialVideoAssetType),
		audioAssetType:    stringDefault(settings.SD2MaterialAudioAssetType, defaultSD2MaterialAudioAssetType),
		createGroupBody:   strings.TrimSpace(settings.SD2MaterialCreateGroupBody),
		createBody:        strings.TrimSpace(settings.SD2MaterialCreateBody),
		getBody:           strings.TrimSpace(settings.SD2MaterialGetBody),
		httpClient:        httpClient,
	}, true, nil
}

func (c *sd2MaterialClient) createAsset(ctx context.Context, sourceURL, mediaKind string) (assetID string, raw string, err error) {
	body, err := c.buildCreateBody(sourceURL, mediaKind)
	if err != nil {
		return "", "", err
	}
	raw, err = c.doOpenAPI(ctx, c.createAction, body)
	if err != nil {
		return "", raw, err
	}
	if err := openAPIError(raw); err != nil {
		return "", raw, err
	}
	assetID = firstJSON(raw,
		"Result.AssetId", "Result.AssetID", "Result.asset_id", "Result.Id", "Result.id",
		"Result.Asset.AssetId", "Result.Asset.AssetID", "Result.Asset.asset_id",
		"Data.AssetId", "Data.AssetID", "Data.asset_id", "Data.Id", "Data.id",
		"AssetId", "AssetID", "asset_id", "Id", "id",
	)
	if strings.TrimSpace(assetID) == "" {
		return "", raw, fmt.Errorf("sd2 material create asset returned empty asset_id")
	}
	return strings.TrimSpace(assetID), raw, nil
}

func (c *sd2MaterialClient) ensureAssetGroupID(ctx context.Context, channelID int) (string, error) {
	row, err := model.EnsureSD2MaterialGroupRow(channelID, c.assetGroupName)
	if err != nil {
		return "", err
	}
	if row.Status == model.SD2MaterialGroupStatusReady && strings.TrimSpace(row.AssetGroupID) != "" {
		return row.AssetGroupID, nil
	}
	groupID, raw, err := c.createAssetGroup(ctx)
	if err != nil {
		_ = model.MarkSD2MaterialGroupFailed(row.Id, err.Error(), truncateForDB(raw))
		return "", err
	}
	_ = model.MarkSD2MaterialGroupReady(row.Id, groupID, truncateForDB(raw))
	return groupID, nil
}

func (c *sd2MaterialClient) createAssetGroup(ctx context.Context) (groupID string, raw string, err error) {
	body, err := c.buildCreateGroupBody()
	if err != nil {
		return "", "", err
	}
	raw, err = c.doOpenAPI(ctx, c.createGroupAction, body)
	if err != nil {
		return "", raw, err
	}
	if err := openAPIError(raw); err != nil {
		return "", raw, err
	}
	groupID = firstJSON(raw,
		"Result.AssetGroupId", "Result.AssetGroupID", "Result.asset_group_id", "Result.GroupId", "Result.GroupID", "Result.group_id", "Result.Id", "Result.id",
		"Data.AssetGroupId", "Data.AssetGroupID", "Data.asset_group_id", "Data.GroupId", "Data.GroupID", "Data.group_id", "Data.Id", "Data.id",
		"AssetGroupId", "AssetGroupID", "asset_group_id", "GroupId", "GroupID", "group_id", "Id", "id",
	)
	if strings.TrimSpace(groupID) == "" {
		return "", raw, fmt.Errorf("sd2 material create group returned empty asset_group_id")
	}
	return strings.TrimSpace(groupID), raw, nil
}

func (c *sd2MaterialClient) waitAssetActive(ctx context.Context, assetID string) (status string, raw string, err error) {
	deadline := time.Now().Add(c.pollTimeout)
	for {
		body, bodyErr := c.buildGetBody(assetID)
		if bodyErr != nil {
			return status, raw, bodyErr
		}
		raw, err = c.doOpenAPI(ctx, c.getAction, body)
		if err != nil {
			return status, raw, err
		}
		if apiErr := openAPIError(raw); apiErr != nil {
			return status, raw, apiErr
		}
		status = normalizeMaterialStatus(firstJSON(raw,
			"Result.Status", "Result.status", "Result.AssetStatus", "Result.asset_status",
			"Result.Asset.Status", "Result.Asset.status", "Result.Asset.AssetStatus", "Result.Asset.asset_status",
			"Data.Status", "Data.status", "Data.AssetStatus", "Data.asset_status",
			"Status", "status", "AssetStatus", "asset_status",
		))
		switch status {
		case model.SD2MaterialStatusActive:
			return status, raw, nil
		case model.SD2MaterialStatusFailed:
			return status, raw, fmt.Errorf("sd2 material asset %s failed: %s", assetID, firstJSON(raw, "Result.Reason", "Result.Message", "Data.Reason", "Data.Message", "Message", "message"))
		}
		if time.Now().After(deadline) {
			return model.SD2MaterialStatusTimeout, raw, fmt.Errorf("sd2 material asset %s active timeout", assetID)
		}
		select {
		case <-ctx.Done():
			return status, raw, ctx.Err()
		case <-time.After(c.pollInterval):
		}
	}
}

func (c *sd2MaterialClient) doOpenAPI(ctx context.Context, action string, body []byte) (string, error) {
	endpoint, err := url.Parse(strings.TrimRight(c.endpoint, "/"))
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("Action", action)
	query.Set("Version", c.version)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if err := signVolcOpenAPIRequest(req, body, c.accessKeyID, c.secretAccessKey, c.region, c.serviceName); err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
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
		return raw, fmt.Errorf("sd2 material openapi status %d: %s", resp.StatusCode, raw)
	}
	return raw, nil
}

func (c *sd2MaterialClient) buildCreateBody(sourceURL, mediaKind string) ([]byte, error) {
	assetType := c.assetType(mediaKind)
	if assetType == "" {
		return nil, fmt.Errorf("sd2 material asset type for media kind %q is not configured", mediaKind)
	}
	if c.createBody != "" {
		return applySD2MaterialTemplate(c.createBody, map[string]string{
			"source_url":     sourceURL,
			"asset_group_id": c.assetGroupID,
			"group_id":       c.assetGroupID,
			"project_name":   c.projectName,
			"asset_type":     assetType,
			"media_kind":     mediaKind,
			"url_hash":       common.Sha256Hex([]byte(sourceURL)),
		})
	}
	body := map[string]interface{}{
		"AssetType": assetType,
	}
	if strings.TrimSpace(c.projectName) != "" {
		body["URL"] = sourceURL
		body["GroupId"] = c.assetGroupID
	} else {
		body["Url"] = sourceURL
		body["AssetGroupId"] = c.assetGroupID
	}
	putSD2MaterialProjectName(body, c.projectName)
	return common.Marshal(body)
}

func (c *sd2MaterialClient) buildCreateGroupBody() ([]byte, error) {
	if c.createGroupBody != "" {
		return applySD2MaterialTemplate(c.createGroupBody, map[string]string{
			"asset_group_name": c.assetGroupName,
			"group_name":       c.assetGroupName,
			"project_name":     c.projectName,
		})
	}
	body := map[string]interface{}{
		"Name": c.assetGroupName,
	}
	putSD2MaterialProjectName(body, c.projectName)
	return common.Marshal(body)
}

func (c *sd2MaterialClient) buildGetBody(assetID string) ([]byte, error) {
	if c.getBody != "" {
		return applySD2MaterialTemplate(c.getBody, map[string]string{
			"asset_id":       assetID,
			"id":             assetID,
			"asset_group_id": c.assetGroupID,
			"group_id":       c.assetGroupID,
			"project_name":   c.projectName,
		})
	}
	body := map[string]interface{}{}
	if strings.TrimSpace(c.projectName) != "" {
		body["Id"] = assetID
	} else {
		body["AssetId"] = assetID
	}
	putSD2MaterialProjectName(body, c.projectName)
	return common.Marshal(body)
}

func putSD2MaterialProjectName(body map[string]interface{}, projectName string) {
	if body == nil {
		return
	}
	if projectName = strings.TrimSpace(projectName); projectName != "" {
		body["ProjectName"] = projectName
	}
}

func (c *sd2MaterialClient) assetType(mediaKind string) string {
	switch strings.ToLower(strings.TrimSpace(mediaKind)) {
	case "video":
		return c.videoAssetType
	case "audio":
		return c.audioAssetType
	default:
		return c.imageAssetType
	}
}

func applySD2MaterialTemplate(template string, values map[string]string) ([]byte, error) {
	out := template
	for k, v := range values {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	var js interface{}
	if err := common.Unmarshal([]byte(out), &js); err != nil {
		return nil, fmt.Errorf("invalid sd2 material body template: %w", err)
	}
	return []byte(out), nil
}

func signVolcOpenAPIRequest(req *http.Request, body []byte, accessKeyID, secretAccessKey, region, serviceName string) error {
	payloadHash := sha256.Sum256(body)
	hexPayloadHash := hex.EncodeToString(payloadHash[:])
	now := time.Now().UTC()
	xDate := now.Format("20060102T150405Z")
	shortDate := now.Format("20060102")

	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", hexPayloadHash)

	canonicalQueryString := canonicalQuery(req.URL.Query())
	headersToSign := map[string]string{
		"content-type":     req.Header.Get("Content-Type"),
		"host":             req.URL.Host,
		"x-content-sha256": hexPayloadHash,
		"x-date":           xDate,
	}
	signedHeaderKeys := make([]string, 0, len(headersToSign))
	for k := range headersToSign {
		signedHeaderKeys = append(signedHeaderKeys, k)
	}
	sort.Strings(signedHeaderKeys)

	var canonicalHeaders strings.Builder
	for _, k := range signedHeaderKeys {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(headersToSign[k]))
		canonicalHeaders.WriteString("\n")
	}
	signedHeaders := strings.Join(signedHeaderKeys, ";")
	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method,
		emptyPathAsSlash(req.URL.EscapedPath()),
		canonicalQueryString,
		canonicalHeaders.String(),
		signedHeaders,
		hexPayloadHash,
	)
	hashedCanonicalRequest := sha256.Sum256([]byte(canonicalRequest))
	credentialScope := fmt.Sprintf("%s/%s/%s/request", shortDate, region, serviceName)
	stringToSign := fmt.Sprintf("HMAC-SHA256\n%s\n%s\n%s",
		xDate,
		credentialScope,
		hex.EncodeToString(hashedCanonicalRequest[:]),
	)

	kDate := hmacSHA256([]byte(secretAccessKey), []byte(shortDate))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(serviceName))
	kSigning := hmacSHA256(kService, []byte("request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf("HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKeyID,
		credentialScope,
		signedHeaders,
		signature,
	))
	return nil
}

func SignVolcOpenAPIRequest(req *http.Request, body []byte, accessKeyID, secretAccessKey, region, serviceName string) error {
	return signVolcOpenAPIRequest(req, body, accessKeyID, secretAccessKey, region, serviceName)
}

func canonicalQuery(query url.Values) string {
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0)
	for _, k := range keys {
		values := append([]string(nil), query[k]...)
		sort.Strings(values)
		for _, v := range values {
			parts = append(parts, fmt.Sprintf("%s=%s", url.QueryEscape(k), url.QueryEscape(v)))
		}
	}
	return strings.Join(parts, "&")
}

func openAPIError(raw string) error {
	for _, path := range []string{
		"ResponseMetadata.Error.Message",
		"ResponseMetadata.Error.Code",
		"Error.Message",
		"Error.Code",
		"error.message",
		"error.code",
	} {
		if value := strings.TrimSpace(gjson.Get(raw, path).String()); value != "" {
			return fmt.Errorf("sd2 material openapi error: %s", value)
		}
	}
	return nil
}

func firstJSON(raw string, paths ...string) string {
	for _, path := range paths {
		result := gjson.Get(raw, path)
		if result.Exists() {
			value := strings.TrimSpace(result.String())
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func normalizeMaterialStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "pending", "creating", "processing", "reviewing", "review", "submitted", "init":
		return model.SD2MaterialStatusPending
	case "active", "success", "succeeded", "completed", "available", "enabled", "normal":
		return model.SD2MaterialStatusActive
	case "failed", "fail", "error", "rejected", "disabled", "deleted", "invalid":
		return model.SD2MaterialStatusFailed
	default:
		return model.SD2MaterialStatusPending
	}
}

// assetURL renders the stable upstream reference for a material asset.
// Seedance must always be handed asset://<asset_id>: the temporary signed URL
// that CreateAsset/GetAsset echoes back expires, so persisting or forwarding it
// makes the reference unusable on any later request.
func assetURL(assetID string) string {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(assetID), "asset://") {
		return assetID
	}
	return "asset://" + assetID
}

func stringDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func intDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func emptyPathAsSlash(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func hmacSHA256(key []byte, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func truncateForDB(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4000 {
		return value
	}
	return value[:4000]
}

func IsSD2MaterialNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}
