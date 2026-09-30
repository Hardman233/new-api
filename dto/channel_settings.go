package dto

type ChannelSettings struct {
	ForceFormat            bool   `json:"force_format,omitempty"`
	ThinkingToContent      bool   `json:"thinking_to_content,omitempty"`
	Proxy                  string `json:"proxy"`
	PassThroughBodyEnabled bool   `json:"pass_through_body_enabled,omitempty"`
	SystemPrompt           string `json:"system_prompt,omitempty"`
	SystemPromptOverride   bool   `json:"system_prompt_override,omitempty"`
}

type VertexKeyType string

const (
	VertexKeyTypeJSON   VertexKeyType = "json"
	VertexKeyTypeAPIKey VertexKeyType = "api_key"
)

type AwsKeyType string

const (
	AwsKeyTypeAKSK   AwsKeyType = "ak_sk"
	AwsKeyTypeApiKey AwsKeyType = "api_key"
)

type ChannelOtherSettings struct {
	AzureResponsesVersion                 string        `json:"azure_responses_version,omitempty"`
	VertexKeyType                         VertexKeyType `json:"vertex_key_type,omitempty"`
	OpenRouterEnterprise                  *bool         `json:"openrouter_enterprise,omitempty"`
	ClaudeBetaQuery                       bool          `json:"claude_beta_query,omitempty"`
	AllowServiceTier                      bool          `json:"allow_service_tier,omitempty"`
	AllowInferenceGeo                     bool          `json:"allow_inference_geo,omitempty"`
	AllowSpeed                            bool          `json:"allow_speed,omitempty"`
	AllowSafetyIdentifier                 bool          `json:"allow_safety_identifier,omitempty"`
	DisableStore                          bool          `json:"disable_store,omitempty"`
	AllowIncludeObfuscation               bool          `json:"allow_include_obfuscation,omitempty"`
	GeminiReferenceImageMax               int           `json:"gemini_reference_image_max,omitempty"`
	GeminiReferenceImageMaxFileSizeMB     int           `json:"gemini_reference_image_max_file_size_mb,omitempty"`
	AwsKeyType                            AwsKeyType    `json:"aws_key_type,omitempty"`
	UpstreamModelUpdateCheckEnabled       bool          `json:"upstream_model_update_check_enabled,omitempty"`
	UpstreamModelUpdateAutoSyncEnabled    bool          `json:"upstream_model_update_auto_sync_enabled,omitempty"`
	UpstreamModelUpdateLastCheckTime      int64         `json:"upstream_model_update_last_check_time,omitempty"`
	UpstreamModelUpdateLastDetectedModels []string      `json:"upstream_model_update_last_detected_models,omitempty"`
	UpstreamModelUpdateLastRemovedModels  []string      `json:"upstream_model_update_last_removed_models,omitempty"`
	UpstreamModelUpdateIgnoredModels      []string      `json:"upstream_model_update_ignored_models,omitempty"`

	SD2MaterialEnabled           bool     `json:"sd2_material_enabled,omitempty"`
	SD2MaterialAccessKeyID       string   `json:"sd2_material_access_key_id,omitempty"`
	SD2MaterialSecretAccessKey   string   `json:"sd2_material_secret_access_key,omitempty"`
	SD2MaterialAssetGroupID      string   `json:"sd2_material_asset_group_id,omitempty"`
	SD2MaterialAssetGroupName    string   `json:"sd2_material_asset_group_name,omitempty"`
	SD2MaterialProjectName       string   `json:"sd2_material_project_name,omitempty"`
	SD2MaterialEndpoint          string   `json:"sd2_material_endpoint,omitempty"`
	SD2MaterialRegion            string   `json:"sd2_material_region,omitempty"`
	SD2MaterialService           string   `json:"sd2_material_service,omitempty"`
	SD2MaterialVersion           string   `json:"sd2_material_version,omitempty"`
	SD2MaterialCreateGroupAction string   `json:"sd2_material_create_group_action,omitempty"`
	SD2MaterialCreateAction      string   `json:"sd2_material_create_action,omitempty"`
	SD2MaterialGetAction         string   `json:"sd2_material_get_action,omitempty"`
	SD2MaterialPollTimeoutSec    int      `json:"sd2_material_poll_timeout_sec,omitempty"`
	SD2MaterialPollIntervalSec   int      `json:"sd2_material_poll_interval_sec,omitempty"`
	SD2MaterialImageAssetType    string   `json:"sd2_material_image_asset_type,omitempty"`
	SD2MaterialVideoAssetType    string   `json:"sd2_material_video_asset_type,omitempty"`
	SD2MaterialAudioAssetType    string   `json:"sd2_material_audio_asset_type,omitempty"`
	SD2MaterialCreateGroupBody   string   `json:"sd2_material_create_group_body,omitempty"`
	SD2MaterialCreateBody        string   `json:"sd2_material_create_body,omitempty"`
	SD2MaterialGetBody           string   `json:"sd2_material_get_body,omitempty"`
	SeedanceRouteTags            []string `json:"seedance_route_tags,omitempty"`
}

func (s *ChannelOtherSettings) IsOpenRouterEnterprise() bool {
	if s == nil || s.OpenRouterEnterprise == nil {
		return false
	}
	return *s.OpenRouterEnterprise
}
