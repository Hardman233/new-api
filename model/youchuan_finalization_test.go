package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYouchuanFinalizationLeaseIsExclusiveAndIdempotent(t *testing.T) {
	taskID := "task-youchuan-finalization-lease"
	t.Cleanup(func() {
		DB.Where("task_id = ?", taskID).Delete(&YouchuanTaskFinalization{})
		DB.Where("task_id = ?", taskID).Delete(&Task{})
	})
	task := &Task{
		TaskID:   taskID,
		Platform: constant.TaskPlatform("58"),
		Status:   TaskStatusSuccess,
		PrivateData: TaskPrivateData{
			Youchuan: &YouchuanTaskPrivateData{ClientCallbackURL: "https://example.com/callback"},
		},
	}
	require.NoError(t, insertTaskForYouchuanTest(t, task))
	require.NoError(t, EnsureYouchuanTaskFinalization(taskID, true))

	claim, claimed, _, err := ClaimYouchuanTaskFinalization(taskID, true, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotEmpty(t, claim)
	var claimedRow YouchuanTaskFinalization
	require.NoError(t, DB.Where("task_id = ?", taskID).First(&claimedRow).Error)
	require.NoError(t, RenewYouchuanTaskFinalization(taskID, claim, 2*time.Minute))
	var renewedRow YouchuanTaskFinalization
	require.NoError(t, DB.Where("task_id = ?", taskID).First(&renewedRow).Error)
	require.Greater(t, renewedRow.ClaimUntil, claimedRow.ClaimUntil)
	require.Error(t, RenewYouchuanTaskFinalization(taskID, "wrong-claim", 2*time.Minute))
	_, claimedAgain, _, err := ClaimYouchuanTaskFinalization(taskID, true, time.Minute)
	require.NoError(t, err)
	require.False(t, claimedAgain)
	require.NoError(t, CompleteYouchuanTaskFinalization(taskID, claim, time.Now().Unix()))
	_, claimedAfterComplete, row, err := ClaimYouchuanTaskFinalization(taskID, true, time.Minute)
	require.NoError(t, err)
	require.False(t, claimedAfterComplete)
	require.True(t, row.Completed)
}

func TestYouchuanFinalizationReleaseSchedulesRetryAndRejectsLostLease(t *testing.T) {
	taskID := "task-youchuan-finalization-retry"
	t.Cleanup(func() {
		DB.Where("task_id = ?", taskID).Delete(&YouchuanTaskFinalization{})
		DB.Where("task_id = ?", taskID).Delete(&Task{})
	})
	task := &Task{
		TaskID:   taskID,
		Platform: constant.TaskPlatform("58"),
		Status:   TaskStatusSuccess,
		PrivateData: TaskPrivateData{
			Youchuan: &YouchuanTaskPrivateData{},
		},
	}
	require.NoError(t, insertTaskForYouchuanTest(t, task))
	require.NoError(t, EnsureYouchuanTaskFinalization(taskID, false))

	claim, claimed, _, err := ClaimYouchuanTaskFinalization(taskID, false, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Error(t, MarkYouchuanTaskOSSCompleted(taskID, "wrong-claim", time.Now().Unix()))
	require.NoError(t, ReleaseYouchuanTaskFinalization(taskID, claim, "temporary OSS failure"))

	var row YouchuanTaskFinalization
	require.NoError(t, DB.Where("task_id = ?", taskID).First(&row).Error)
	assert.Equal(t, 1, row.AttemptCount)
	assert.NotEmpty(t, row.LastError)
	assert.Greater(t, row.NextRetryAt, time.Now().Unix())

	require.NoError(t, DB.Model(&YouchuanTaskFinalization{}).Where("task_id = ?", taskID).Update("next_retry_at", 0).Error)
	nextClaim, claimed, _, err := ClaimYouchuanTaskFinalization(taskID, false, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotEqual(t, claim, nextClaim)
	require.Error(t, CompleteYouchuanTaskFinalization(taskID, claim, time.Now().Unix()))
	require.NoError(t, CompleteYouchuanTaskFinalization(taskID, nextClaim, time.Now().Unix()))
}

func insertTaskForYouchuanTest(t *testing.T, task *Task) error {
	t.Helper()
	task.CreatedAt = time.Now().Unix()
	task.UpdatedAt = time.Now().Unix()
	return DB.Create(task).Error
}
