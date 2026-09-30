package model

import (
	"github.com/QuantumNous/new-api/common"
)

// body_storage 取值
const (
	AuditBodyInline      = "inline"       // 纯文本正文全量存本表 body 字段
	AuditBodyLocal       = "local"        // 二进制只落了本地盘、OSS 还没传成功
	AuditBodyLocalOSS    = "local+oss"    // 二进制本地 + OSS 双副本（稳态）
	AuditBodyOmittedMeta = "omitted_meta" // 纯二进制只记元信息，无正文
	AuditBodyAbandoned   = "abandoned"    // OSS 补传多次失败放弃
)

// ApiAuditLog 请求/响应正文审计留存。
// 纯文本正文全量存 RequestBody/ResponseBody(LONGTEXT)；
// 二进制正文走本地盘 + OSS，本表仅记元信息 + 双路径。
type ApiAuditLog struct {
	Id uint64 `json:"id" gorm:"primaryKey"`

	CreatedAt int64 `json:"created_at" gorm:"bigint;index"`
	ExpireAt  int64 `json:"expire_at" gorm:"bigint;index"`

	RequestId         string `json:"request_id" gorm:"type:varchar(64);index;default:''"`
	UpstreamRequestId string `json:"upstream_request_id" gorm:"type:varchar(128);index;default:''"`

	UserId    int    `json:"user_id" gorm:"index"`
	Username  string `json:"username" gorm:"type:varchar(64);default:''"`
	TokenId   int    `json:"token_id" gorm:"index"`
	TokenName string `json:"token_name" gorm:"type:varchar(128);default:''"`
	UserGroup string `json:"user_group" gorm:"type:varchar(64);index;default:''"`

	ModelName   string `json:"model_name" gorm:"type:varchar(128);index;default:''"`
	ChannelId   int    `json:"channel_id" gorm:"index"`
	ChannelName string `json:"channel_name" gorm:"type:varchar(128);default:''"`
	ChannelType int    `json:"channel_type"`
	RelayMode   int    `json:"relay_mode"`

	Method     string `json:"method" gorm:"type:varchar(16);default:''"`
	Path       string `json:"path" gorm:"type:varchar(255);default:''"`
	StatusCode int    `json:"status_code"`
	IsStream   bool   `json:"is_stream"`
	UseTimeMs  int    `json:"use_time_ms"`
	ClientIp   string `json:"client_ip" gorm:"type:varchar(64);default:''"`

	RequestContentType  string `json:"request_content_type" gorm:"type:varchar(128);default:''"`
	ResponseContentType string `json:"response_content_type" gorm:"type:varchar(128);default:''"`

	// 纯文本正文（脱敏、可选加密后）全量存这两个字段
	RequestBody  string `json:"request_body,omitempty" gorm:"type:text"`
	ResponseBody string `json:"response_body,omitempty" gorm:"type:text"`

	RequestBodySize    int64  `json:"request_body_size"`
	ResponseBodySize   int64  `json:"response_body_size"`
	RequestBodySha256  string `json:"request_body_sha256" gorm:"type:char(64);default:''"`
	ResponseBodySha256 string `json:"response_body_sha256" gorm:"type:char(64);default:''"`
	RequestTruncated   bool   `json:"request_truncated"`
	ResponseTruncated  bool   `json:"response_truncated"`

	// 正文位置标记 + 二进制场景下的双路径
	BodyStorage       string `json:"body_storage" gorm:"type:varchar(16);index;default:'inline'"`
	RequestOssKey     string `json:"request_oss_key" gorm:"type:varchar(512);default:''"`
	ResponseOssKey    string `json:"response_oss_key" gorm:"type:varchar(512);default:''"`
	RequestLocalPath  string `json:"request_local_path" gorm:"type:varchar(512);default:''"`
	ResponseLocalPath string `json:"response_local_path" gorm:"type:varchar(512);default:''"`

	Error string `json:"error" gorm:"type:text"`
	Other string `json:"other" gorm:"type:text"`
}

// Insert 写入审计日志，返回带主键的记录。
func (l *ApiAuditLog) Insert() error {
	return LOG_DB.Create(l).Error
}

// UpdateOssLocation 上传成功后更新某一侧（req/resp）的 OSS key 与 body_storage。
func UpdateAuditOssLocation(id uint64, kind, ossKey, bodyStorage string) error {
	field := "request_oss_key"
	if kind == "resp" {
		field = "response_oss_key"
	}
	return LOG_DB.Model(&ApiAuditLog{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			field:          ossKey,
			"body_storage": bodyStorage,
		}).Error
}

// UpdateAuditBodyStorage 仅更新 body_storage（如标记 abandoned）。
func UpdateAuditBodyStorage(id uint64, bodyStorage string) error {
	return LOG_DB.Model(&ApiAuditLog{}).Where("id = ?", id).
		Update("body_storage", bodyStorage).Error
}

// GetApiAuditLogById 按主键取单条。
func GetApiAuditLogById(id uint64) (*ApiAuditLog, error) {
	var l ApiAuditLog
	err := LOG_DB.Where("id = ?", id).First(&l).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// GetApiAuditLogByRequestId returns one audit row without request/response bodies.
func GetApiAuditLogByRequestId(requestId string) (*ApiAuditLog, error) {
	var l ApiAuditLog
	err := LOG_DB.Omit("request_body", "response_body").
		Where("request_id = ?", requestId).
		Order("id desc").
		First(&l).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// CleanupExpiredAuditLogs 删除一批过期审计日志，返回删除条数。
func CleanupExpiredAuditLogs(limit int) (int64, error) {
	now := common.GetTimestamp()
	// 子查询限量删除，兼容三种数据库：先取一批 id 再删。
	var ids []uint64
	if err := LOG_DB.Model(&ApiAuditLog{}).
		Where("expire_at > 0 AND expire_at < ?", now).
		Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res := LOG_DB.Where("id IN ?", ids).Delete(&ApiAuditLog{})
	return res.RowsAffected, res.Error
}

// AuditLogQuery 列表查询条件。
type AuditLogQuery struct {
	StartTimestamp int64
	EndTimestamp   int64
	UserId         int
	TokenId        int
	ModelName      string
	ChannelId      int
	RequestId      string
	StatusCode     int
}

// GetAuditLogs 分页查询审计日志列表（不返回正文，省带宽）。
func GetAuditLogs(q AuditLogQuery, startIdx, pageSize int) ([]*ApiAuditLog, int64, error) {
	tx := LOG_DB.Model(&ApiAuditLog{})
	if q.StartTimestamp > 0 {
		tx = tx.Where("created_at >= ?", q.StartTimestamp)
	}
	if q.EndTimestamp > 0 {
		tx = tx.Where("created_at <= ?", q.EndTimestamp)
	}
	if q.UserId > 0 {
		tx = tx.Where("user_id = ?", q.UserId)
	}
	if q.TokenId > 0 {
		tx = tx.Where("token_id = ?", q.TokenId)
	}
	if q.ModelName != "" {
		tx = tx.Where("model_name = ?", q.ModelName)
	}
	if q.ChannelId > 0 {
		tx = tx.Where("channel_id = ?", q.ChannelId)
	}
	if q.RequestId != "" {
		tx = tx.Where("request_id = ?", q.RequestId)
	}
	if q.StatusCode > 0 {
		tx = tx.Where("status_code = ?", q.StatusCode)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []*ApiAuditLog
	// 列表不返回正文，只取元信息列。
	err := tx.Omit("request_body", "response_body").
		Order("id desc").Offset(startIdx).Limit(pageSize).Find(&logs).Error
	return logs, total, err
}

// DeleteAuditLogById 删除单条审计日志。
func DeleteAuditLogById(id uint64) error {
	return LOG_DB.Where("id = ?", id).Delete(&ApiAuditLog{}).Error
}
