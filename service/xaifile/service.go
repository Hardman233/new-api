package xaifile

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/oss"
)

const (
	localFileIDPrefix = "file_newapi_"
	defaultMaxFileMB  = 512
	copyBufferSize    = 256 * 1024
)

func IsLocalFileID(fileID string) bool {
	return strings.HasPrefix(strings.TrimSpace(fileID), localFileIDPrefix)
}

func Store(ctx context.Context, userID, tokenID int, filename string, expiresAfter *int64, reader io.Reader) (*model.XAIFile, error) {
	filename = sanitizeFilename(filename)
	if filename == "" {
		return nil, errors.New("filename is required")
	}
	if reader == nil {
		return nil, errors.New("file content is required")
	}
	fileID, err := newFileID()
	if err != nil {
		return nil, err
	}
	root := storageRoot()
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("create xAI file directory: %w", err)
	}
	temporary, err := os.CreateTemp(root, ".upload-*")
	if err != nil {
		return nil, fmt.Errorf("create xAI upload: %w", err)
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()

	hash := sha256.New()
	maxBytes := int64(common.GetEnvOrDefault("XAI_FILES_MAX_MB", defaultMaxFileMB)) * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = int64(defaultMaxFileMB) * 1024 * 1024
	}
	written, err := io.CopyBuffer(io.MultiWriter(temporary, hash), io.LimitReader(reader, maxBytes+1), make([]byte, copyBufferSize))
	if err != nil {
		return nil, fmt.Errorf("store xAI file: %w", err)
	}
	if written > maxBytes {
		return nil, fmt.Errorf("file exceeds XAI_FILES_MAX_MB=%d", maxBytes/(1024*1024))
	}
	if err = temporary.Sync(); err != nil {
		return nil, fmt.Errorf("flush xAI file: %w", err)
	}
	if err = temporary.Close(); err != nil {
		return nil, fmt.Errorf("close xAI file: %w", err)
	}

	contentType, err := detectContentType(temporaryName, filename)
	if err != nil {
		return nil, err
	}
	extension := extensionFor(filename, contentType)
	finalPath := filepath.Join(root, fileID+extension)
	if err = os.Rename(temporaryName, finalPath); err != nil {
		return nil, fmt.Errorf("commit xAI file: %w", err)
	}
	committed = true

	now := time.Now().Unix()
	expiresAt := int64(0)
	if expiresAfter != nil {
		if *expiresAfter <= 0 {
			_ = os.Remove(finalPath)
			return nil, errors.New("expires_after must be positive")
		}
		expiresAt = now + *expiresAfter
	}
	file := &model.XAIFile{
		CreatedAt:    now,
		FileID:       fileID,
		UserID:       userID,
		TokenID:      tokenID,
		Filename:     filename,
		ContentType:  contentType,
		SizeBytes:    written,
		SHA256:       hex.EncodeToString(hash.Sum(nil)),
		LocalPath:    finalPath,
		SourceTaskID: fileID,
		ExpiresAt:    expiresAt,
	}
	if err = uploadToOSSIfEnabled(ctx, file); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	if err = file.Insert(); err != nil {
		if file.OwnsOSSObject && file.OSSKey != "" {
			if uploader, uploadErr := oss.GetUploader(); uploadErr == nil {
				_ = uploader.Delete(ctx, file.OSSKey)
			}
		}
		_ = os.Remove(finalPath)
		return nil, err
	}
	if file.OSSKey != "" && !common.GetEnvOrDefaultBool("XAI_FILES_KEEP_LOCAL", true) {
		_ = os.Remove(finalPath)
		file.LocalPath = ""
		_ = model.DB.Model(&model.XAIFile{}).Where("id = ?", file.ID).Update("local_path", "").Error
	}
	return file, nil
}

func StoreBytes(ctx context.Context, userID, tokenID int, filename string, expiresAfter *int64, data []byte) (*model.XAIFile, error) {
	return Store(ctx, userID, tokenID, filename, expiresAfter, bytesReader(data))
}

func Read(ctx context.Context, file *model.XAIFile) ([]byte, error) {
	if file == nil {
		return nil, model.ErrXAIFileNotFound
	}
	if file.ExpiresAt > 0 && file.ExpiresAt <= time.Now().Unix() {
		return nil, model.ErrXAIFileExpired
	}
	if path := strings.TrimSpace(file.LocalPath); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return data, nil
		}
	}
	if key := strings.TrimSpace(file.OSSKey); key != "" {
		uploader, err := oss.GetUploader()
		if err != nil {
			return nil, err
		}
		return uploader.Get(ctx, key)
	}
	return nil, errors.New("xAI file content is unavailable")
}

func ReadForUser(ctx context.Context, userID int, fileID string) (*model.XAIFile, []byte, error) {
	file, err := model.GetXAIFileForUser(userID, fileID)
	if err != nil {
		return nil, nil, err
	}
	data, err := Read(ctx, file)
	return file, data, err
}

func DataURLForUser(ctx context.Context, userID int, fileID string) (string, error) {
	file, data, err := ReadForUser(ctx, userID, fileID)
	if err != nil {
		return "", err
	}
	contentType := strings.TrimSpace(file.ContentType)
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func TextContentForUser(ctx context.Context, userID int, fileID string) (*model.XAIFile, []byte, error) {
	file, data, err := ReadForUser(ctx, userID, fileID)
	if err != nil {
		return nil, nil, err
	}
	if !strings.HasPrefix(strings.ToLower(file.ContentType), "text/") && !utf8.Valid(data) {
		return nil, nil, errors.New("text download format is not supported for this file")
	}
	return file, data, nil
}

func Delete(ctx context.Context, userID int, fileID string) (*model.XAIFile, error) {
	file, err := model.DeleteXAIFileForUser(userID, fileID)
	if err != nil {
		return nil, err
	}
	if file.LocalPath != "" {
		_ = os.Remove(file.LocalPath)
	}
	if file.OwnsOSSObject && file.OSSKey != "" {
		if uploader, uploadErr := oss.GetUploader(); uploadErr == nil {
			_ = uploader.Delete(ctx, file.OSSKey)
		}
	}
	return file, nil
}

func CreatePublicURL(userID int, fileID, baseURL string, expiresAfter *int64) (*model.XAIFile, error) {
	file, err := model.GetXAIFileForUser(userID, fileID)
	if err != nil {
		return nil, err
	}
	if !supportsPublicURL(file.ContentType) {
		return nil, errors.New("public URLs are only supported for images, videos, and PDFs")
	}
	now := time.Now().Unix()
	if file.PublicURL != "" && file.PublicToken != "" &&
		(file.PublicURLExpiresAt == 0 || file.PublicURLExpiresAt > now) {
		if expiresAfter != nil {
			if *expiresAfter <= 0 {
				return nil, errors.New("expires_after must be positive")
			}
			file.PublicURLExpiresAt = now + *expiresAfter
			file.UpdatedAt = now
			if err = model.DB.Model(&model.XAIFile{}).Where("id = ?", file.ID).Updates(map[string]any{
				"public_url_expires_at": file.PublicURLExpiresAt,
				"updated_at":            now,
			}).Error; err != nil {
				return nil, err
			}
		}
		return file, nil
	}
	token, err := common.GenerateRandomCharsKey(48)
	if err != nil {
		return nil, err
	}
	expiresAt := int64(0)
	if expiresAfter != nil {
		if *expiresAfter <= 0 {
			return nil, errors.New("expires_after must be positive")
		}
		expiresAt = time.Now().Unix() + *expiresAfter
	} else if file.ExpiresAt > 0 {
		expiresAt = file.ExpiresAt
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("XAI_PUBLIC_BASE_URL")), "/")
	}
	if baseURL == "" {
		return nil, errors.New("XAI_PUBLIC_BASE_URL is required to create a public URL")
	}
	publicURL := baseURL + "/xai/files/" + url.PathEscape(file.FileID) + "/content?token=" + url.QueryEscape(token)
	return model.UpdateXAIFilePublicURL(userID, fileID, token, publicURL, expiresAt)
}

func supportsPublicURL(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "video/") ||
		contentType == "application/pdf"
}

func EnsureTaskOutput(ctx context.Context, task *model.Task, baseURL string) (*model.XAIFile, error) {
	if task == nil || task.PrivateData.XAI == nil || task.PrivateData.XAI.StorageOptions == nil {
		return nil, nil
	}
	if task.Status != model.TaskStatusSuccess {
		return nil, nil
	}
	options := task.PrivateData.XAI.StorageOptions
	if task.PrivateData.XAI.OutputFileID != "" {
		file, err := model.GetXAIFileForUser(task.UserId, task.PrivateData.XAI.OutputFileID)
		if err != nil {
			return nil, err
		}
		return finalizeTaskOutput(task, file, options, baseURL)
	}
	if existing, err := model.GetXAIFileBySourceTask(task.UserId, task.TaskID); err == nil {
		return finalizeTaskOutput(task, existing, options, baseURL)
	} else if !errors.Is(err, model.ErrXAIFileNotFound) {
		return nil, err
	}
	resultURL := strings.TrimSpace(task.GetResultURL())
	if resultURL == "" {
		return nil, errors.New("completed xAI task has no result URL")
	}
	data, contentType, err := downloadTaskResult(ctx, resultURL)
	if err != nil {
		return nil, err
	}
	filename := strings.TrimSpace(options.Filename)
	if filename == "" {
		filename = task.TaskID + extensionFor("", contentType)
	}
	file, err := Store(ctx, task.UserId, task.PrivateData.TokenId, filename, options.ExpiresAfter, bytesReader(data))
	if err != nil {
		return nil, err
	}
	file.SourceTaskID = task.TaskID
	if err = model.DB.Model(&model.XAIFile{}).Where("id = ?", file.ID).Update("source_task_id", task.TaskID).Error; err != nil {
		_, _ = Delete(ctx, task.UserId, file.FileID)
		if existing, lookupErr := model.GetXAIFileBySourceTask(task.UserId, task.TaskID); lookupErr == nil {
			return finalizeTaskOutput(task, existing, options, baseURL)
		}
		return nil, err
	}
	return finalizeTaskOutput(task, file, options, baseURL)
}

func finalizeTaskOutput(task *model.Task, file *model.XAIFile, options *model.XAIStorageOptions, baseURL string) (*model.XAIFile, error) {
	if task == nil || file == nil {
		return file, nil
	}
	var err error
	if options != nil && options.CreatePublicURL &&
		(file.PublicURL == "" || (file.PublicURLExpiresAt > 0 && file.PublicURLExpiresAt <= time.Now().Unix())) {
		file, err = CreatePublicURL(task.UserId, file.FileID, baseURL, options.PublicURLExpiresAfter)
		if err != nil {
			return nil, err
		}
	}
	if err = model.UpdateTaskXAIOutputFileID(task, file.FileID); err != nil {
		return nil, err
	}
	return file, nil
}

func PublicFile(ctx context.Context, fileID, token string) (*model.XAIFile, []byte, error) {
	file, err := model.GetXAIFileByPublicToken(fileID, token)
	if err != nil {
		return nil, nil, err
	}
	data, err := Read(ctx, file)
	return file, data, err
}

func storageRoot() string {
	root := strings.TrimSpace(os.Getenv("XAI_FILES_DIR"))
	if root == "" {
		root = "/data/xai-files"
	}
	return filepath.Clean(root)
}

func newFileID() (string, error) {
	suffix, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		return "", err
	}
	return localFileIDPrefix + suffix, nil
}

func sanitizeFilename(filename string) string {
	filename = filepath.Base(strings.TrimSpace(filename))
	filename = strings.ReplaceAll(filename, "\x00", "")
	if filename == "." || filename == string(filepath.Separator) {
		return ""
	}
	return filename
}

func detectContentType(path, filename string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	buffer := make([]byte, 512)
	count, err := bufio.NewReader(file).Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	contentType := http.DetectContentType(buffer[:count])
	if byExtension := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); byExtension != "" && contentType == "application/octet-stream" {
		contentType = byExtension
	}
	return contentType, nil
}

func extensionFor(filename, contentType string) string {
	if extension := strings.ToLower(filepath.Ext(filename)); extension != "" && len(extension) <= 12 {
		return extension
	}
	if extensions, err := mime.ExtensionsByType(contentType); err == nil && len(extensions) > 0 {
		return extensions[0]
	}
	return ".bin"
}

func uploadToOSSIfEnabled(ctx context.Context, file *model.XAIFile) error {
	if !common.GetEnvOrDefaultBool("XAI_FILES_OSS_ENABLED", true) || !common.AuditLogOSSConfigured() {
		return nil
	}
	uploader, err := oss.GetUploader()
	if err != nil {
		return err
	}
	input, err := os.Open(file.LocalPath)
	if err != nil {
		return err
	}
	defer input.Close()
	prefix := strings.Trim(strings.TrimSpace(common.AuditOSSKeyPrefix), "/")
	key := strings.Join([]string{prefix, "xai-files", time.Now().Format("2006/01/02"), file.FileID + extensionFor(file.Filename, file.ContentType)}, "/")
	if err = uploader.PutReader(ctx, key, input, file.ContentType); err != nil {
		return fmt.Errorf("upload xAI file to OSS: %w", err)
	}
	file.OSSKey = key
	file.OwnsOSSObject = true
	return nil
}

func downloadTaskResult(ctx context.Context, rawURL string) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{Timeout: time.Duration(common.GetEnvOrDefault("XAI_FILES_DOWNLOAD_TIMEOUT_SEC", 180)) * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download task result returned HTTP %d", response.StatusCode)
	}
	maxBytes := int64(common.GetEnvOrDefault("XAI_FILES_MAX_MB", defaultMaxFileMB)) * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return nil, "", errors.New("task result exceeds XAI_FILES_MAX_MB")
	}
	contentType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, nil
}

type sliceReader struct {
	data []byte
	off  int
}

func bytesReader(data []byte) io.Reader { return &sliceReader{data: data} }

func (r *sliceReader) Read(buffer []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	count := copy(buffer, r.data[r.off:])
	r.off += count
	return count, nil
}

func ParsePositiveSeconds(value string) (*int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return nil, errors.New("expires_after must be a positive integer")
	}
	return &seconds, nil
}
