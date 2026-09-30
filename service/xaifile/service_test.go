package xaifile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestEnsureTaskOutputRecoversPublicURLWithoutDuplicateFile(t *testing.T) {
	oldDB := model.DB
	database, err := gorm.Open(sqlite.Open("file:xai-task-output-recovery?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.DB = database
	t.Cleanup(func() { model.DB = oldDB })
	if err = database.AutoMigrate(&model.Task{}, &model.XAIFile{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAI_FILES_DIR", t.TempDir())
	t.Setenv("XAI_FILES_OSS_ENABLED", "false")
	t.Setenv("XAI_PUBLIC_BASE_URL", "")

	media := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "video/mp4")
		_, _ = writer.Write([]byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0x00, 0x00, 0x00, 0x00})
	}))
	t.Cleanup(media.Close)

	task := &model.Task{
		TaskID: "task_storage_recovery",
		UserId: 7,
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			TokenId:   11,
			ResultURL: media.URL + "/result.mp4",
			XAI: &model.XAITaskPrivateData{StorageOptions: &model.XAIStorageOptions{
				Filename:        "result.mp4",
				CreatePublicURL: true,
			}},
		},
	}
	if err = task.Insert(); err != nil {
		t.Fatal(err)
	}

	if _, err = EnsureTaskOutput(context.Background(), task, ""); err == nil {
		t.Fatal("expected missing public base URL to fail")
	}
	var afterFailure int64
	if err = database.Model(&model.XAIFile{}).Where("source_task_id = ?", task.TaskID).Count(&afterFailure).Error; err != nil {
		t.Fatal(err)
	}
	if afterFailure != 1 {
		t.Fatalf("stored files after first attempt = %d, want 1", afterFailure)
	}

	file, err := EnsureTaskOutput(context.Background(), task, "https://files.example.com")
	if err != nil {
		t.Fatalf("recovered EnsureTaskOutput() error = %v", err)
	}
	if file == nil || file.PublicURL == "" || task.PrivateData.XAI.OutputFileID != file.FileID {
		t.Fatalf("unexpected recovered file: file=%+v task=%+v", file, task.PrivateData.XAI)
	}
	var finalCount int64
	if err = database.Model(&model.XAIFile{}).Where("source_task_id = ?", task.TaskID).Count(&finalCount).Error; err != nil {
		t.Fatal(err)
	}
	if finalCount != 1 {
		t.Fatalf("stored files after recovery = %d, want 1", finalCount)
	}
}

func TestCreatePublicURLRejectsUnsupportedContentType(t *testing.T) {
	oldDB := model.DB
	database, err := gorm.Open(sqlite.Open("file:xai-public-url-type?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.DB = database
	t.Cleanup(func() { model.DB = oldDB })
	if err = database.AutoMigrate(&model.XAIFile{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAI_FILES_DIR", t.TempDir())
	t.Setenv("XAI_FILES_OSS_ENABLED", "false")
	file, err := StoreBytes(context.Background(), 7, 11, "notes.txt", nil, []byte("plain text"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CreatePublicURL(7, file.FileID, "https://files.example.com", nil); err == nil {
		t.Fatal("expected public URL creation for text file to fail")
	}
}
