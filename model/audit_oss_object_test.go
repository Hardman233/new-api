package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditOssObjectPendingIsUniquePerAuditLogAndBodyKind(t *testing.T) {
	truncateTables(t)

	params := AuditOssObjectParams{
		AuditLogId: 10,
		RequestId:  "req-oss-object",
		BodyKind:   "resp",
		LocalPath:  "spool/resp.bin",
		OssKey:     "audit/resp.bin",
		SizeBytes:  123,
		Sha256:     "abc",
	}

	require.NoError(t, EnsureAuditOssObjectPending(params))
	require.NoError(t, EnsureAuditOssObjectPending(params))

	var count int64
	require.NoError(t, DB.Model(&AuditOssObject{}).Where("audit_log_id = ? AND body_kind = ?", 10, "resp").Count(&count).Error)
	assert.EqualValues(t, 1, count)

	require.NoError(t, MarkAuditOssObjectUploaded(10, "resp", false))

	var row AuditOssObject
	require.NoError(t, DB.Where("audit_log_id = ? AND body_kind = ?", 10, "resp").First(&row).Error)
	assert.Equal(t, AuditOssStatusUploaded, row.Status)
	assert.NotZero(t, row.UploadedAt)
	assert.NotZero(t, row.CheckedAt)
}
