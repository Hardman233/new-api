package common

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 审计 spool 目录名（独立于请求体 body-cache，避免被其 5 分钟清理误删）。
const auditSpoolDir = "new-api-audit-spool"

// GetAuditSpoolDir 返回审计落盘目录。复用磁盘缓存根路径，未配置时退回临时目录。
func GetAuditSpoolDir() string {
	base := GetDiskCachePath()
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, auditSpoolDir)
}

// EnsureAuditSpoolDir 确保 spool 目录存在（0700）。
func EnsureAuditSpoolDir() error {
	return os.MkdirAll(GetAuditSpoolDir(), 0700)
}

// WriteAuditSpoolFile 把（已压缩加密的）二进制正文落盘，返回文件路径。
// kind: req | resp。
func WriteAuditSpoolFile(kind string, auditLogId uint64, data []byte) (string, error) {
	if err := EnsureAuditSpoolDir(); err != nil {
		return "", fmt.Errorf("failed to create audit spool dir: %w", err)
	}
	dir := GetAuditSpoolDir()
	filename := fmt.Sprintf("%s-%d-%s.audit", kind, auditLogId, uuid.New().String()[:8])
	filePath := filepath.Join(dir, filename)

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create audit spool file: %w", err)
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		os.Remove(filePath)
		return "", fmt.Errorf("failed to write audit spool file: %w", err)
	}
	if err = file.Close(); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("failed to close audit spool file: %w", err)
	}
	return filePath, nil
}

// ReadAuditSpoolFile 读取落盘文件。
func ReadAuditSpoolFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// RemoveAuditSpoolFile 删除落盘文件（幂等，不存在不报错）。
func RemoveAuditSpoolFile(path string) error {
	err := os.Remove(path)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

// CleanupOldAuditSpoolFiles 删除超过 maxAge 的 spool 文件。
// 仅在容量超限时调用，避免误删仍作为快路径的本地副本。
func CleanupOldAuditSpoolFiles(maxAge time.Duration) error {
	dir := GetAuditSpoolDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// GetAuditSpoolUsage 返回 spool 目录文件数与总字节数。
func GetAuditSpoolUsage() (fileCount int, totalSize int64, err error) {
	dir := GetAuditSpoolDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		fileCount++
		totalSize += info.Size()
	}
	return fileCount, totalSize, nil
}

// ===== 共享的压缩 / 加密 / 哈希 helper（纯文本入库与二进制落盘都用）=====

// Sha256Hex 计算十六进制 sha256。
func Sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// GzipCompress gzip 压缩。
func GzipCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GzipDecompress gzip 解压。
func GzipDecompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// auditAESKey 从 AuditLogSecret 派生 32 字节 AES-256 密钥。
func auditAESKey() ([]byte, error) {
	if AuditLogSecret == "" {
		return nil, errors.New("AUDIT_LOG_SECRET is empty")
	}
	sum := sha256.Sum256([]byte(AuditLogSecret))
	return sum[:], nil
}

// AuditEncrypt 用 AES-256-GCM 加密；nonce 前置于密文。
func AuditEncrypt(plain []byte) ([]byte, error) {
	key, err := auditAESKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

// AuditDecrypt 解密 AuditEncrypt 的输出。
func AuditDecrypt(enc []byte) ([]byte, error) {
	key, err := auditAESKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(enc) < ns {
		return nil, errors.New("audit ciphertext too short")
	}
	nonce, ciphertext := enc[:ns], enc[ns:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// AuditEncodeStored 把加密字节编码为可存入 LONGTEXT 的字符串（base64）。
func AuditEncodeStored(enc []byte) string {
	return "enc:" + base64.StdEncoding.EncodeToString(enc)
}

// AuditDecodeStored 还原 AuditEncodeStored 的输出；非加密内容原样返回。
func AuditDecodeStored(stored string) ([]byte, bool, error) {
	if !strings.HasPrefix(stored, "enc:") {
		return []byte(stored), false, nil
	}
	raw, err := base64.StdEncoding.DecodeString(stored[len("enc:"):])
	if err != nil {
		return nil, true, err
	}
	return raw, true, nil
}

// auditRedactPatterns 文本脱敏正则：密钥、Bearer、手机号、邮箱。
var auditRedactPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._-]{20,}`),
	regexp.MustCompile(`1[3-9]\d{9}`),
	regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),
}

const auditRedacted = "***REDACTED***"

// RedactAuditText 对文本正文做正则脱敏。字段名级脱敏在调用方按需补充。
func RedactAuditText(body []byte) []byte {
	out := body
	for _, re := range auditRedactPatterns {
		out = re.ReplaceAll(out, []byte(auditRedacted))
	}
	return out
}

// 内联 base64 字段名（命中即剥离），以及 data: 前缀的内联资源。
var auditBase64FieldNames = map[string]struct{}{
	"b64_json":           {},
	"bytesbase64encoded": {},
	"image":              {},
	"audio":              {},
	"video":              {},
	"data":               {},
}

// auditInlineBase64MinLen 超过此长度的疑似 base64 值才剥离，避免误伤短文本。
const auditInlineBase64MinLen = 512

// StripInlineBase64 递归遍历 JSON，把命中字段名或 data: 前缀的超长 base64 值
// 替换为 ***BINARY_OMITTED(size=N,sha256=...)*** 占位，返回新 JSON 与是否发生剥离。
// 非 JSON 内容原样返回。
func StripInlineBase64(body []byte) ([]byte, bool) {
	var root any
	if err := Unmarshal(body, &root); err != nil {
		return body, false
	}
	changed := false
	walked := walkStripBase64(root, "", &changed)
	if !changed {
		return body, false
	}
	out, err := Marshal(walked)
	if err != nil {
		return body, false
	}
	return out, true
}

func walkStripBase64(node any, key string, changed *bool) any {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			v[k] = walkStripBase64(child, k, changed)
		}
		return v
	case []any:
		for i := range v {
			v[i] = walkStripBase64(v[i], key, changed)
		}
		return v
	case string:
		if shouldStripBase64Value(key, v) {
			*changed = true
			return base64OmittedPlaceholder([]byte(v))
		}
		return v
	default:
		return node
	}
}

func shouldStripBase64Value(key, val string) bool {
	if len(val) < auditInlineBase64MinLen {
		return false
	}
	if strings.HasPrefix(val, "data:") && strings.Contains(val, ";base64,") {
		return true
	}
	_, hit := auditBase64FieldNames[strings.ToLower(key)]
	return hit
}

func base64OmittedPlaceholder(raw []byte) string {
	return fmt.Sprintf("***BINARY_OMITTED(size=%d,sha256=%s)***", len(raw), Sha256Hex(raw))
}
