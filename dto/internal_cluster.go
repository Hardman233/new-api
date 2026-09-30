package dto

type InternalAuthVerifyRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type InternalAuthVerifyData struct {
	DomesticUserID int    `json:"domestic_user_id"`
	Username       string `json:"username"`
	DisplayName    string `json:"display_name"`
	Email          string `json:"email"`
	Role           int    `json:"role"`
	Status         int    `json:"status"`
	Group          string `json:"group"`
}

type InternalBillingLedgerImportRequest struct {
	SourceRegion       string                 `json:"source_region"`
	SourceNode         string                 `json:"source_node"`
	SourceBillingID    string                 `json:"source_billing_id"`
	SourceUserID       int                    `json:"source_user_id"`
	DomesticUserID     int                    `json:"domestic_user_id"`
	BillingType        string                 `json:"billing_type"`
	SourceRequestID    string                 `json:"source_request_id"`
	SourceTaskID       string                 `json:"source_task_id"`
	Username           string                 `json:"username"`
	TokenID            int                    `json:"token_id"`
	TokenName          string                 `json:"token_name"`
	UserGroup          string                 `json:"user_group"`
	ChannelID          int                    `json:"channel_id"`
	ChannelName        string                 `json:"channel_name"`
	SupplierName       string                 `json:"supplier_name"`
	ModelName          string                 `json:"model_name"`
	UpstreamModel      string                 `json:"upstream_model_name"`
	Quota              int64                  `json:"quota"`
	Currency           string                 `json:"currency"`
	ExchangeRate       string                 `json:"exchange_rate,omitempty"`
	AmountCNYReference string                 `json:"amount_cny_reference,omitempty"`
	Status             string                 `json:"status"`
	UsageSource        string                 `json:"usage_source"`
	IsStream           bool                   `json:"is_stream"`
	PromptTokens       int64                  `json:"prompt_tokens"`
	CompletionTokens   int64                  `json:"completion_tokens"`
	RequestHash        string                 `json:"request_hash"`
	ResponseHash       string                 `json:"response_hash"`
	CreatedAt          int64                  `json:"created_at"`
	SettledAt          int64                  `json:"settled_at"`
	Other              map[string]interface{} `json:"other"`
}
