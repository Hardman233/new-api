package model

import (
	"strings"

	"gorm.io/gorm"
)

type UpstreamResponseLog struct {
	Id uint64 `json:"id" gorm:"primaryKey"`

	CreatedAt int64 `json:"created_at" gorm:"bigint;index"`

	RequestId string `json:"request_id" gorm:"type:varchar(64);not null;default:'';index"`
	BillingID string `json:"billing_id" gorm:"type:varchar(192);not null;default:'';index"`
	TaskID    string `json:"task_id" gorm:"type:varchar(128);not null;default:'';index"`

	UserID    int    `json:"user_id" gorm:"not null;default:0;index"`
	Username  string `json:"username" gorm:"type:varchar(64);not null;default:''"`
	TokenID   int    `json:"token_id" gorm:"not null;default:0;index"`
	TokenName string `json:"token_name" gorm:"type:varchar(128);not null;default:''"`
	UserGroup string `json:"user_group" gorm:"type:varchar(64);not null;default:'';index"`

	ModelName         string `json:"model_name" gorm:"type:varchar(128);not null;default:'';index"`
	UpstreamModelName string `json:"upstream_model_name" gorm:"type:varchar(128);not null;default:'';index"`
	ServerRegion      string `json:"server_region" gorm:"type:varchar(32);not null;default:'';index"`
	ChannelID         int    `json:"channel_id" gorm:"not null;default:0;index"`
	ChannelName       string `json:"channel_name" gorm:"type:varchar(128);not null;default:''"`
	ChannelType       int    `json:"channel_type" gorm:"not null;default:0"`
	RelayMode         int    `json:"relay_mode" gorm:"not null;default:0"`
	RetryCount        int    `json:"retry_count" gorm:"not null;default:0"`

	Method      string `json:"method" gorm:"type:varchar(16);not null;default:''"`
	Path        string `json:"path" gorm:"type:varchar(255);not null;default:''"`
	StatusCode  int    `json:"status_code" gorm:"not null;default:0;index"`
	ContentType string `json:"content_type" gorm:"type:varchar(128);not null;default:''"`

	Body              string `json:"body,omitempty" gorm:"type:text"`
	BodySize          int64  `json:"body_size" gorm:"not null;default:0"`
	SanitizedBodySize int64  `json:"sanitized_body_size" gorm:"not null;default:0"`
	BodySha256        string `json:"body_sha256" gorm:"type:char(64);not null;default:''"`
	BodyTruncated     bool   `json:"body_truncated" gorm:"not null;default:false"`
	MediaReplaced     bool   `json:"media_replaced" gorm:"not null;default:false"`

	UsageJSON        string `json:"usage_json,omitempty" gorm:"type:text"`
	PromptTokens     int64  `json:"prompt_tokens" gorm:"not null;default:0"`
	CompletionTokens int64  `json:"completion_tokens" gorm:"not null;default:0"`
	TotalTokens      int64  `json:"total_tokens" gorm:"not null;default:0"`

	Error string `json:"error" gorm:"type:text"`
	Other string `json:"other" gorm:"type:text"`
}

func (UpstreamResponseLog) TableName() string {
	return "upstream_response_logs"
}

func (l *UpstreamResponseLog) Insert() error {
	return LOG_DB.Create(l).Error
}

func GetLatestUpstreamResponseLog(billingID, requestID, taskID string) (*UpstreamResponseLog, error) {
	billingID = strings.TrimSpace(billingID)
	requestID = strings.TrimSpace(requestID)
	taskID = strings.TrimSpace(taskID)
	if billingID == "" && requestID == "" && taskID == "" {
		return nil, gorm.ErrRecordNotFound
	}

	tx := LOG_DB.Model(&UpstreamResponseLog{})
	first := true
	addOr := func(query string, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		if first {
			tx = tx.Where(query, value)
			first = false
			return
		}
		tx = tx.Or(query, value)
	}
	addOr("billing_id = ?", billingID)
	addOr("request_id = ?", requestID)
	addOr("task_id = ?", taskID)

	var row UpstreamResponseLog
	err := tx.Order("id desc").First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}
