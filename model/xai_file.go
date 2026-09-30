package model

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	ErrXAIFileNotFound      = errors.New("xAI file not found")
	ErrXAIFileExpired       = errors.New("xAI file expired")
	ErrXAIFileInvalidFilter = errors.New("invalid xAI file filter")
)

type XAIFile struct {
	ID                 int64  `json:"-" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt          int64  `json:"created_at" gorm:"index"`
	UpdatedAt          int64  `json:"updated_at"`
	FileID             string `json:"id" gorm:"type:varchar(64);uniqueIndex"`
	UserID             int    `json:"-" gorm:"index"`
	TokenID            int    `json:"-" gorm:"index"`
	Filename           string `json:"filename" gorm:"type:varchar(512)"`
	ContentType        string `json:"content_type" gorm:"type:varchar(191)"`
	SizeBytes          int64  `json:"size"`
	SHA256             string `json:"sha256" gorm:"type:char(64);index"`
	LocalPath          string `json:"-" gorm:"type:text"`
	OSSKey             string `json:"-" gorm:"type:text"`
	OwnsOSSObject      bool   `json:"-"`
	SourceTaskID       string `json:"-" gorm:"type:varchar(191);uniqueIndex"`
	ExpiresAt          int64  `json:"expires_at,omitempty" gorm:"index"`
	PublicToken        string `json:"-" gorm:"type:varchar(96);index"`
	PublicURL          string `json:"public_url,omitempty" gorm:"type:text"`
	PublicURLExpiresAt int64  `json:"public_url_expires_at,omitempty" gorm:"index"`
	DeletedAt          int64  `json:"-" gorm:"index"`
}

type XAIFileListParams struct {
	UserID     int
	Limit      int
	Offset     int
	Descending bool
	SortBy     string
	Filter     string
}

func (XAIFile) TableName() string { return "xai_files" }

func (f *XAIFile) Insert() error {
	now := time.Now().Unix()
	if f.CreatedAt == 0 {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	return DB.Create(f).Error
}

func GetXAIFileForUser(userID int, fileID string) (*XAIFile, error) {
	var file XAIFile
	err := DB.Where("user_id = ? AND file_id = ? AND deleted_at = 0", userID, strings.TrimSpace(fileID)).First(&file).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrXAIFileNotFound
		}
		return nil, err
	}
	if file.ExpiresAt > 0 && file.ExpiresAt <= time.Now().Unix() {
		return nil, ErrXAIFileExpired
	}
	return &file, nil
}

func GetXAIFileBySourceTask(userID int, taskID string) (*XAIFile, error) {
	var file XAIFile
	err := DB.Where("user_id = ? AND source_task_id = ? AND deleted_at = 0", userID, strings.TrimSpace(taskID)).First(&file).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrXAIFileNotFound
		}
		return nil, err
	}
	if file.ExpiresAt > 0 && file.ExpiresAt <= time.Now().Unix() {
		return nil, ErrXAIFileExpired
	}
	return &file, nil
}

func GetXAIFileByPublicToken(fileID, token string) (*XAIFile, error) {
	var file XAIFile
	err := DB.Where("file_id = ? AND public_token = ? AND deleted_at = 0", strings.TrimSpace(fileID), strings.TrimSpace(token)).First(&file).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrXAIFileNotFound
		}
		return nil, err
	}
	now := time.Now().Unix()
	if file.ExpiresAt > 0 && file.ExpiresAt <= now {
		return nil, ErrXAIFileExpired
	}
	if file.PublicToken == "" || (file.PublicURLExpiresAt > 0 && file.PublicURLExpiresAt <= now) {
		return nil, ErrXAIFileExpired
	}
	return &file, nil
}

func ListXAIFiles(params XAIFileListParams) ([]*XAIFile, bool, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	query := DB.Where("user_id = ? AND deleted_at = 0 AND (expires_at = 0 OR expires_at > ?)", params.UserID, time.Now().Unix())
	if filter := strings.TrimSpace(params.Filter); filter != "" {
		clause, args, filterErr := parseXAIFileFilter(filter)
		if filterErr != nil {
			return nil, false, fmt.Errorf("%w: %v", ErrXAIFileInvalidFilter, filterErr)
		}
		query = query.Where(clause, args...)
	}
	column := "created_at"
	switch params.SortBy {
	case "filename":
		column = "filename"
	case "size":
		column = "size_bytes"
	}
	direction := "ASC"
	if params.Descending {
		direction = "DESC"
	}
	var files []*XAIFile
	err := query.Order(column + " " + direction).Order("id " + direction).Offset(params.Offset).Limit(limit + 1).Find(&files).Error
	if err != nil {
		return nil, false, err
	}
	hasMore := len(files) > limit
	if hasMore {
		files = files[:limit]
	}
	return files, hasMore, nil
}

func DeleteXAIFileForUser(userID int, fileID string) (*XAIFile, error) {
	file, err := GetXAIFileForUser(userID, fileID)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if err = DB.Model(&XAIFile{}).Where("id = ? AND deleted_at = 0", file.ID).Updates(map[string]any{
		"deleted_at": now,
		"updated_at": now,
	}).Error; err != nil {
		return nil, err
	}
	file.DeletedAt = now
	return file, nil
}

func UpdateXAIFilePublicURL(userID int, fileID, token, publicURL string, expiresAt int64) (*XAIFile, error) {
	file, err := GetXAIFileForUser(userID, fileID)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	updates := map[string]any{
		"public_token":          token,
		"public_url":            publicURL,
		"public_url_expires_at": expiresAt,
		"updated_at":            now,
	}
	if err = DB.Model(&XAIFile{}).Where("id = ?", file.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	file.PublicToken = token
	file.PublicURL = publicURL
	file.PublicURLExpiresAt = expiresAt
	file.UpdatedAt = now
	return file, nil
}

func RevokeXAIFilePublicURL(userID int, fileID string) (*XAIFile, string, error) {
	file, err := GetXAIFileForUser(userID, fileID)
	if err != nil {
		return nil, "", err
	}
	previous := file.PublicURL
	now := time.Now().Unix()
	if err = DB.Model(&XAIFile{}).Where("id = ?", file.ID).Updates(map[string]any{
		"public_token":          "",
		"public_url":            "",
		"public_url_expires_at": 0,
		"updated_at":            now,
	}).Error; err != nil {
		return nil, "", err
	}
	file.PublicToken = ""
	file.PublicURL = ""
	file.PublicURLExpiresAt = 0
	return file, previous, nil
}

func EncodeXAIFilePaginationOffset(offset int) string {
	if offset <= 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func DecodeXAIFilePaginationOffset(token string) (int, error) {
	if strings.TrimSpace(token) == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, err
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, errors.New("invalid pagination token")
	}
	return offset, nil
}
