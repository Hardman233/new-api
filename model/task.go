package model

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	commonRelay "github.com/QuantumNous/new-api/relay/common"
	"gorm.io/gorm"
)

type TaskStatus string

func (t TaskStatus) ToVideoStatus() string {
	var status string
	switch t {
	case TaskStatusQueued, TaskStatusSubmitted:
		status = dto.VideoStatusQueued
	case TaskStatusInProgress:
		status = dto.VideoStatusInProgress
	case TaskStatusSuccess:
		status = dto.VideoStatusCompleted
	case TaskStatusFailure:
		status = dto.VideoStatusFailed
	default:
		status = dto.VideoStatusUnknown // Default fallback
	}
	return status
}

const (
	TaskStatusNotStart   TaskStatus = "NOT_START"
	TaskStatusSubmitted             = "SUBMITTED"
	TaskStatusQueued                = "QUEUED"
	TaskStatusInProgress            = "IN_PROGRESS"
	TaskStatusFailure               = "FAILURE"
	TaskStatusSuccess               = "SUCCESS"
	TaskStatusUnknown               = "UNKNOWN"
)

type Task struct {
	ID             int64                 `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt      int64                 `json:"created_at" gorm:"index"`
	UpdatedAt      int64                 `json:"updated_at"`
	TaskID         string                `json:"task_id" gorm:"type:varchar(191);index"`                                             // 第三方id，不一定有/ song id\ Task id
	Platform       constant.TaskPlatform `json:"platform" gorm:"type:varchar(30);index;index:idx_task_platform_upstream,priority:1"` // 平台
	UserId         int                   `json:"user_id" gorm:"index"`
	Group          string                `json:"group" gorm:"type:varchar(50)"` // 修正计费用
	ChannelId      int                   `json:"channel_id" gorm:"index"`
	Quota          int                   `json:"quota"`
	Action         string                `json:"action" gorm:"type:varchar(40);index"` // 任务类型, song, lyrics, description-mode
	Status         TaskStatus            `json:"status" gorm:"type:varchar(20);index"` // 任务状态
	FailReason     string                `json:"fail_reason"`
	SubmitTime     int64                 `json:"submit_time" gorm:"index"`
	StartTime      int64                 `json:"start_time" gorm:"index"`
	FinishTime     int64                 `json:"finish_time" gorm:"index"`
	Progress       string                `json:"progress" gorm:"type:varchar(20);index"`
	UpstreamTaskID string                `json:"-" gorm:"type:varchar(191);index:idx_task_platform_upstream,priority:2"`
	Properties     Properties            `json:"properties" gorm:"type:json"`
	Username       string                `json:"username,omitempty" gorm:"-"`
	// 禁止返回给用户，内部可能包含key等隐私信息
	PrivateData TaskPrivateData `json:"-" gorm:"column:private_data;type:json"`
	Data        json.RawMessage `json:"data" gorm:"type:json"`
}

func (t *Task) SetData(data any) {
	b, _ := common.Marshal(data)
	t.Data = json.RawMessage(b)
}

func (t *Task) GetData(v any) error {
	return common.Unmarshal(t.Data, &v)
}

type Properties struct {
	Input             string `json:"input"`
	UpstreamModelName string `json:"upstream_model_name,omitempty"`
	OriginModelName   string `json:"origin_model_name,omitempty"`
	CreateRequestID   string `json:"create_request_id,omitempty"`
}

func (m *Properties) Scan(val interface{}) error {
	bytesValue, _ := val.([]byte)
	if len(bytesValue) == 0 {
		*m = Properties{}
		return nil
	}
	return common.Unmarshal(bytesValue, m)
}

func (m Properties) Value() (driver.Value, error) {
	if m == (Properties{}) {
		return nil, nil
	}
	return common.Marshal(m)
}

type TaskPrivateData struct {
	Key                string `json:"key,omitempty"`
	OssResultURL       string `json:"oss_result_url,omitempty"`
	OssResultKey       string `json:"oss_result_key,omitempty"`
	OssResultSizeBytes int64  `json:"oss_result_size_bytes,omitempty"`
	UpstreamTaskID     string `json:"upstream_task_id,omitempty"` // 上游真实 task ID
	ResultURL          string `json:"result_url,omitempty"`       // 任务成功后的结果 URL（视频地址等）
	SubmitRetryCount   int    `json:"submit_retry_count,omitempty"`
	// 计费上下文：用于异步退款/差额结算（轮询阶段读取）
	BillingSource  string                   `json:"billing_source,omitempty"`  // "wallet" 或 "subscription"
	SubscriptionId int                      `json:"subscription_id,omitempty"` // 订阅 ID，用于订阅退款
	TokenId        int                      `json:"token_id,omitempty"`        // 令牌 ID，用于令牌额度退款
	BillingContext *TaskBillingContext      `json:"billing_context,omitempty"` // 计费参数快照（用于轮询阶段重新计算）
	XAI            *XAITaskPrivateData      `json:"xai,omitempty"`
	Youchuan       *YouchuanTaskPrivateData `json:"youchuan,omitempty"`
}

type YouchuanTaskPrivateData struct {
	Action                string               `json:"action,omitempty"`
	ParentUpstreamTaskID  string               `json:"parent_upstream_task_id,omitempty"`
	CredentialFingerprint string               `json:"credential_fingerprint,omitempty"`
	ClientCallbackURL     string               `json:"client_callback_url,omitempty"`
	RawCost               json.RawMessage      `json:"raw_cost,omitempty"`
	Seed                  uint64               `json:"seed,omitempty"`
	Outputs               []YouchuanTaskOutput `json:"outputs,omitempty"`
	CallbackDeliveredAt   int64                `json:"callback_delivered_at,omitempty"`
}

type YouchuanTaskOutput struct {
	Index        int    `json:"index"`
	RemoteURL    string `json:"remote_url,omitempty"`
	URL          string `json:"url,omitempty"`
	OSSKey       string `json:"oss_key,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	MediaKind    string `json:"media_kind,omitempty"`
	Audit        string `json:"audit,omitempty"`
	UploadStatus string `json:"upload_status,omitempty"`
	UploadError  string `json:"upload_error,omitempty"`
}

// ApplyYouchuanResponseMediaSizes adds platform media sizes without changing
// the official Youchuan urls string-array shape.
func ApplyYouchuanResponseMediaSizes(data []byte, outputs []YouchuanTaskOutput) ([]byte, error) {
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	urls, ok := response["urls"].([]any)
	if !ok || len(urls) == 0 {
		return data, nil
	}

	sizes := make([]int64, len(urls))
	hasSize := false
	if existing, ok := response["size_bytes"].([]any); ok {
		for index := range sizes {
			if index < len(existing) {
				if size, ok := youchuanResponseSize(existing[index]); ok && size > 0 {
					sizes[index] = size
					hasSize = true
				}
			}
		}
	}
	for _, output := range outputs {
		if output.Index < 0 || output.Index >= len(sizes) || output.SizeBytes <= 0 {
			continue
		}
		sizes[output.Index] = output.SizeBytes
		hasSize = true
	}
	if !hasSize {
		return data, nil
	}
	response["size_bytes"] = sizes
	return json.Marshal(response)
}

func youchuanResponseSize(value any) (int64, bool) {
	switch number := value.(type) {
	case float64:
		return int64(number), number > 0
	case float32:
		return int64(number), number > 0
	case int:
		return int64(number), number > 0
	case int64:
		return number, number > 0
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil && parsed > 0
	default:
		return 0, false
	}
}

type XAITaskPrivateData struct {
	Operation       string             `json:"operation,omitempty"`
	Duration        int                `json:"duration,omitempty"`
	AspectRatio     string             `json:"aspect_ratio,omitempty"`
	Resolution      string             `json:"resolution,omitempty"`
	OutputUploadURL string             `json:"output_upload_url,omitempty"`
	StorageOptions  *XAIStorageOptions `json:"storage_options,omitempty"`
	OutputFileID    string             `json:"output_file_id,omitempty"`
	DeferredStatus  string             `json:"deferred_status,omitempty"`
	StorageError    string             `json:"storage_error,omitempty"`
}

type XAIStorageOptions struct {
	Filename              string `json:"filename,omitempty"`
	ExpiresAfter          *int64 `json:"expires_after,omitempty"`
	CreatePublicURL       bool   `json:"create_public_url,omitempty"`
	PublicURLExpiresAfter *int64 `json:"public_url_expires_after,omitempty"`
}

// TaskBillingContext 记录任务提交时的计费参数，以便轮询阶段可以重新计算额度。
type TaskBillingContext struct {
	ModelPrice      float64            `json:"model_price,omitempty"`       // 模型单价
	GroupRatio      float64            `json:"group_ratio,omitempty"`       // 分组倍率
	ModelRatio      float64            `json:"model_ratio,omitempty"`       // 模型倍率
	OtherRatios     map[string]float64 `json:"other_ratios,omitempty"`      // 附加倍率（时长、分辨率等）
	OriginModelName string             `json:"origin_model_name,omitempty"` // 模型名称，必须为OriginModelName
	PerCallBilling  bool               `json:"per_call_billing,omitempty"`  // 按次计费：跳过轮询阶段的差额结算
	BillingMode     string             `json:"billing_mode,omitempty"`

	TieredBillingSnapshot *billingexpr.BillingSnapshot `json:"tiered_billing_snapshot,omitempty"`
	BillingRequestInput   *billingexpr.RequestInput    `json:"billing_request_input,omitempty"`
}

func (t *Task) sanitizePrivateDataForPersistence() {
	if t == nil || t.PrivateData.BillingContext == nil || t.PrivateData.BillingContext.BillingRequestInput == nil {
		return
	}
	sanitized := billingexpr.SanitizeRequestInput(*t.PrivateData.BillingContext.BillingRequestInput)
	t.PrivateData.BillingContext.BillingRequestInput = &sanitized
}

// BeforeSave covers direct GORM Create/Save calls in addition to the task
// update helpers below.
func (t *Task) BeforeSave(_ *gorm.DB) error {
	t.sanitizePrivateDataForPersistence()
	return nil
}

// GetUpstreamTaskID 获取上游真实 task ID（用于与 provider 通信）
// 旧数据没有 UpstreamTaskID 时，TaskID 本身就是上游 ID
func (t *Task) GetUpstreamTaskID() string {
	if strings.TrimSpace(t.UpstreamTaskID) != "" {
		return strings.TrimSpace(t.UpstreamTaskID)
	}
	if t.PrivateData.UpstreamTaskID != "" {
		return t.PrivateData.UpstreamTaskID
	}
	return t.TaskID
}

func FindTaskByUpstreamID(platform constant.TaskPlatform, upstreamTaskID string, userID int) (*Task, bool, error) {
	tasks, err := FindTasksByUpstreamID(platform, upstreamTaskID, userID)
	if err != nil {
		return nil, false, err
	}
	if len(tasks) == 0 {
		return nil, false, nil
	}
	return tasks[0], true, nil
}

// FindTasksByUpstreamID returns every task candidate that can own an upstream
// ID. Callback handlers use the full candidate set so identical provider IDs
// from different credentials cannot be resolved to the wrong task.
func FindTasksByUpstreamID(platform constant.TaskPlatform, upstreamTaskID string, userID int) ([]*Task, error) {
	upstreamTaskID = strings.TrimSpace(upstreamTaskID)
	if upstreamTaskID == "" {
		return nil, nil
	}
	var tasks []*Task
	seen := make(map[int64]struct{})
	appendMatches := func(matches []*Task) {
		for _, task := range matches {
			if task == nil {
				continue
			}
			if _, exists := seen[task.ID]; exists {
				continue
			}
			seen[task.ID] = struct{}{}
			tasks = append(tasks, task)
		}
	}
	load := func(condition string, args ...any) error {
		query := DB.Where("platform = ?", platform)
		if userID > 0 {
			query = query.Where("user_id = ?", userID)
		}
		var matches []*Task
		if err := query.Where(condition, args...).Order("id desc").Find(&matches).Error; err != nil {
			return err
		}
		appendMatches(matches)
		return nil
	}
	if err := load("upstream_task_id = ?", upstreamTaskID); err != nil {
		return nil, err
	}
	if err := load("task_id = ?", upstreamTaskID); err != nil {
		return nil, err
	}

	// Older tasks stored the upstream ID only inside private_data. Query that
	// JSON field directly instead of scanning a fixed-size recent-task window.
	if err := load(taskPrivateDataUpstreamIDPredicate(), upstreamTaskID); err != nil {
		return nil, err
	}
	return tasks, nil
}

func taskPrivateDataUpstreamIDPredicate() string {
	if common.UsingPostgreSQL {
		return "private_data::jsonb ->> 'upstream_task_id' = ?"
	}
	if common.UsingMySQL {
		return "JSON_UNQUOTE(JSON_EXTRACT(private_data, '$.upstream_task_id')) = ?"
	}
	return "json_extract(private_data, '$.upstream_task_id') = ?"
}

func UpdateYouchuanTaskGeneratedMedia(taskID, sourceURL, ossKey, ossURL string, sizeBytes int64) (bool, error) {
	var task Task
	if err := DB.Where("task_id = ?", strings.TrimSpace(taskID)).First(&task).Error; err != nil {
		return false, err
	}
	if task.PrivateData.Youchuan == nil {
		return false, nil
	}
	updated := false
	for index := range task.PrivateData.Youchuan.Outputs {
		output := &task.PrivateData.Youchuan.Outputs[index]
		if strings.TrimSpace(output.RemoteURL) != strings.TrimSpace(sourceURL) {
			continue
		}
		output.URL = strings.TrimSpace(ossURL)
		output.OSSKey = strings.TrimSpace(ossKey)
		output.SizeBytes = sizeBytes
		output.UploadStatus = GeneratedMediaStatusUploaded
		output.UploadError = ""
		updated = true
	}

	var response map[string]any
	if json.Unmarshal(task.Data, &response) == nil {
		if urls, ok := response["urls"].([]any); ok {
			for index, raw := range urls {
				value, _ := raw.(string)
				if strings.TrimSpace(value) == strings.TrimSpace(sourceURL) {
					urls[index] = strings.TrimSpace(ossURL)
					updated = true
				}
			}
			response["urls"] = urls
			if encoded, err := json.Marshal(response); err == nil {
				task.Data = encoded
			}
		}
	}
	if encoded, err := ApplyYouchuanResponseMediaSizes(task.Data, task.PrivateData.Youchuan.Outputs); err == nil {
		task.Data = encoded
	}
	if !updated {
		return false, nil
	}
	task.sanitizePrivateDataForPersistence()
	return true, DB.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"data":         task.Data,
		"private_data": task.PrivateData,
		"updated_at":   time.Now().Unix(),
	}).Error
}

func UpdateTaskPrivateData(task *Task) error {
	if task == nil || task.ID == 0 {
		return nil
	}
	task.sanitizePrivateDataForPersistence()
	return DB.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"private_data": task.PrivateData,
		"updated_at":   time.Now().Unix(),
	}).Error
}

func UpdateTaskDataAndPrivateData(task *Task) error {
	if task == nil || task.ID == 0 {
		return nil
	}
	task.sanitizePrivateDataForPersistence()
	return DB.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"data":         task.Data,
		"private_data": task.PrivateData,
		"updated_at":   time.Now().Unix(),
	}).Error
}

// GetResultURL 获取任务结果 URL（视频地址等）
// 新数据存在 PrivateData.ResultURL 中；旧数据回退到 FailReason（历史兼容）
func (t *Task) GetResultURL() string {
	if t.PrivateData.OssResultURL != "" {
		return t.PrivateData.OssResultURL
	}
	if t.PrivateData.ResultURL != "" {
		return t.PrivateData.ResultURL
	}
	return t.FailReason
}

// GetOriginalResultURL returns the upstream video URL before generated-media
// rewriting. Proxy URLs used for inline video payloads are not upstream URLs.
func (t *Task) GetOriginalResultURL() string {
	if t == nil {
		return ""
	}
	rawURL := strings.TrimSpace(t.PrivateData.ResultURL)
	if rawURL == "" {
		legacy := strings.TrimSpace(t.FailReason)
		if strings.HasPrefix(legacy, "http://") || strings.HasPrefix(legacy, "https://") {
			rawURL = legacy
		}
	}
	if rawURL == "" || (!strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://")) {
		return ""
	}
	if strings.Contains(rawURL, "/v1/videos/") && strings.Contains(rawURL, "/content") {
		return ""
	}
	return rawURL
}

func UpdateTaskXAIOutputFileID(task *Task, fileID string) error {
	if task == nil {
		return nil
	}
	if task.PrivateData.XAI == nil {
		task.PrivateData.XAI = &XAITaskPrivateData{}
	}
	task.PrivateData.XAI.OutputFileID = fileID
	task.sanitizePrivateDataForPersistence()
	return DB.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"private_data": task.PrivateData,
		"updated_at":   time.Now().Unix(),
	}).Error
}

func UpdateTaskXAIStorageError(task *Task, message string) error {
	if task == nil {
		return nil
	}
	if task.PrivateData.XAI == nil {
		task.PrivateData.XAI = &XAITaskPrivateData{}
	}
	task.PrivateData.XAI.StorageError = message
	task.sanitizePrivateDataForPersistence()
	return DB.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"private_data": task.PrivateData,
		"updated_at":   time.Now().Unix(),
	}).Error
}

// GenerateTaskID 生成对外暴露的 task_xxxx 格式 ID
func GenerateTaskID() string {
	key, _ := common.GenerateRandomCharsKey(32)
	return "task_" + key
}

func (p *TaskPrivateData) Scan(val interface{}) error {
	bytesValue, _ := val.([]byte)
	if len(bytesValue) == 0 {
		return nil
	}
	return common.Unmarshal(bytesValue, p)
}

func (p TaskPrivateData) Value() (driver.Value, error) {
	if (p == TaskPrivateData{}) {
		return nil, nil
	}
	return common.Marshal(p)
}

// SyncTaskQueryParams 用于包含所有搜索条件的结构体，可以根据需求添加更多字段
type SyncTaskQueryParams struct {
	Platform       constant.TaskPlatform
	ChannelID      string
	TaskID         string
	UserID         string
	Action         string
	Status         string
	StartTimestamp int64
	EndTimestamp   int64
	UserIDs        []int
}

func InitTask(platform constant.TaskPlatform, relayInfo *commonRelay.RelayInfo) *Task {
	properties := Properties{}
	privateData := TaskPrivateData{}
	if relayInfo != nil && relayInfo.ChannelMeta != nil {
		if relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeGemini ||
			relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeVertexAi {
			privateData.Key = relayInfo.ChannelMeta.ApiKey
		}
		if relayInfo.UpstreamModelName != "" {
			properties.UpstreamModelName = relayInfo.UpstreamModelName
		}
		if relayInfo.OriginModelName != "" {
			properties.OriginModelName = relayInfo.OriginModelName
		}
	}

	// 使用预生成的公开 ID（如果有），否则新生成
	taskID := ""
	if relayInfo.TaskRelayInfo != nil && relayInfo.TaskRelayInfo.PublicTaskID != "" {
		taskID = relayInfo.TaskRelayInfo.PublicTaskID
	} else {
		taskID = GenerateTaskID()
	}

	t := &Task{
		TaskID:      taskID,
		UserId:      relayInfo.UserId,
		Group:       relayInfo.UsingGroup,
		SubmitTime:  time.Now().Unix(),
		Status:      TaskStatusNotStart,
		Progress:    "0%",
		ChannelId:   relayInfo.ChannelId,
		Platform:    platform,
		Properties:  properties,
		PrivateData: privateData,
	}
	return t
}

func TaskGetAllUserTask(userId int, startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB.Where("user_id = ?", userId)

	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.StartTimestamp != 0 {
		// 假设您已将前端传来的时间戳转换为数据库所需的时间格式，并处理了时间戳的验证和解析
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Omit("channel_id").Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func TaskGetAllTasks(startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB

	// 添加过滤条件
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func GetTimedOutUnfinishedTasks(cutoffUnix int64, limit int) []*Task {
	var tasks []*Task
	err := DB.Where("progress != ?", "100%").
		Where("status NOT IN ?", []string{TaskStatusFailure, TaskStatusSuccess}).
		Where("submit_time < ?", cutoffUnix).
		Order("submit_time").
		Limit(limit).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetAllUnFinishSyncTasks(limit int) []*Task {
	var tasks []*Task
	var err error
	// get all tasks progress is not 100%
	err = DB.Where("progress != ?", "100%").Where("status != ?", TaskStatusFailure).Where("status != ?", TaskStatusSuccess).Limit(limit).Order("id").Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func CountUnfinishedTasksByChannel(channelID int) (int64, error) {
	if channelID <= 0 {
		return 0, nil
	}
	var count int64
	err := DB.Model(&Task{}).
		Where("channel_id = ?", channelID).
		Where("progress != ?", "100%").
		Where("status NOT IN ?", []string{TaskStatusFailure, TaskStatusSuccess}).
		Count(&count).Error
	return count, err
}

func GetByOnlyTaskId(taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("task_id = ?", taskId).First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

func GetByTaskId(userId int, taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("user_id = ? and task_id = ?", userId, taskId).
		First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

func GetByTaskIds(userId int, taskIds []any) ([]*Task, error) {
	if len(taskIds) == 0 {
		return nil, nil
	}
	var task []*Task
	var err error
	err = DB.Where("user_id = ? and task_id in (?)", userId, taskIds).
		Find(&task).Error
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (Task *Task) Insert() error {
	var err error
	err = DB.Create(Task).Error
	return err
}

type taskSnapshot struct {
	Status     TaskStatus
	Progress   string
	StartTime  int64
	FinishTime int64
	FailReason string
	ResultURL  string
	Data       json.RawMessage
}

func (s taskSnapshot) Equal(other taskSnapshot) bool {
	return s.Status == other.Status &&
		s.Progress == other.Progress &&
		s.StartTime == other.StartTime &&
		s.FinishTime == other.FinishTime &&
		s.FailReason == other.FailReason &&
		s.ResultURL == other.ResultURL &&
		bytes.Equal(s.Data, other.Data)
}

func (t *Task) Snapshot() taskSnapshot {
	return taskSnapshot{
		Status:     t.Status,
		Progress:   t.Progress,
		StartTime:  t.StartTime,
		FinishTime: t.FinishTime,
		FailReason: t.FailReason,
		ResultURL:  t.PrivateData.ResultURL,
		Data:       t.Data,
	}
}

func (Task *Task) Update() error {
	var err error
	err = DB.Save(Task).Error
	return err
}

func (t *Task) UpdateQuota() error {
	if t == nil || t.ID == 0 {
		return nil
	}
	return DB.Model(&Task{}).Where("id = ?", t.ID).Update("quota", t.Quota).Error
}

// UpdateWithStatus performs a conditional UPDATE guarded by fromStatus (CAS).
// Returns (true, nil) if this caller won the update, (false, nil) if
// another process already moved the task out of fromStatus.
//
// Uses Model().Select("*").Updates() instead of Save() because GORM's Save
// falls back to INSERT ON CONFLICT when the WHERE-guarded UPDATE matches
// zero rows, which silently bypasses the CAS guard.
func (t *Task) UpdateWithStatus(fromStatus TaskStatus) (bool, error) {
	result := DB.Model(t).Where("status = ?", fromStatus).Select("*").Updates(t)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// TaskBulkUpdate performs an unconditional bulk UPDATE by upstream task_id strings.
// Same caveats as TaskBulkUpdateByID — no CAS guard.
func TaskBulkUpdate(taskIds []string, params map[string]any) error {
	if len(taskIds) == 0 {
		return nil
	}
	return DB.Model(&Task{}).
		Where("task_id in (?)", taskIds).
		Updates(params).Error
}

// TaskBulkUpdateByID performs an unconditional bulk UPDATE by primary key IDs.
// WARNING: This function has NO CAS (Compare-And-Swap) guard — it will overwrite
// any concurrent status changes. DO NOT use in billing/quota lifecycle flows
// (e.g., timeout, success, failure transitions that trigger refunds or settlements).
// For status transitions that involve billing, use Task.UpdateWithStatus() instead.
func TaskBulkUpdateByID(ids []int64, params map[string]any) error {
	if len(ids) == 0 {
		return nil
	}
	return DB.Model(&Task{}).
		Where("id in (?)", ids).
		Updates(params).Error
}

type TaskQuotaUsage struct {
	Mode  string  `json:"mode"`
	Count float64 `json:"count"`
}

// TaskCountAllTasks returns total tasks that match the given query params (admin usage)
func TaskCountAllTasks(queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{})
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}

// TaskCountAllUserTask returns total tasks for given user
func TaskCountAllUserTask(userId int, queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{}).Where("user_id = ?", userId)
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}
func (t *Task) ToOpenAIVideo() *dto.OpenAIVideo {
	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = t.TaskID
	openAIVideo.Status = t.Status.ToVideoStatus()
	openAIVideo.BillingID = t.TaskID
	openAIVideo.Model = t.Properties.OriginModelName
	openAIVideo.SetProgressStr(t.Progress)
	openAIVideo.CreatedAt = t.CreatedAt
	openAIVideo.CompletedAt = t.UpdatedAt
	openAIVideo.OriginalURL = t.GetOriginalResultURL()
	openAIVideo.SetMetadata("url", t.GetResultURL())
	return openAIVideo
}
