package xaigrpc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/internal/xaiapi"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

func TestOfficialVideoRPCBridge(t *testing.T) {
	type capturedRequest struct {
		path string
		body map[string]any
	}
	captured := make(chan capturedRequest, 2)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer platform-key" {
			http.Error(writer, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		captured <- capturedRequest{path: request.URL.Path, body: body}
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/extensions") {
			_, _ = writer.Write([]byte(`{"id":"task_extend","status":"queued"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"id":"task_generate","status":"queued"}`))
	})
	connection := testGRPCConnection(t, handler)
	client := xaiapi.NewVideoClient(connection)
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer platform-key")

	duration := int32(12)
	ratio := xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_9_16
	resolution := xaiapi.VideoResolution_VIDEO_RESOLUTION_720P
	response, err := client.GenerateVideo(ctx, &xaiapi.GenerateVideoRequest{
		Prompt:      "animate the image",
		Model:       "grok-imagine-video",
		Duration:    &duration,
		Image:       &xaiapi.ImageUrlContent{Source: &xaiapi.ImageUrlContent_FileId{FileId: "file_image"}},
		AspectRatio: &ratio,
		Resolution:  &resolution,
		ReferenceImages: []*xaiapi.ImageUrlContent{
			{Source: &xaiapi.ImageUrlContent_ImageUrl{ImageUrl: "https://example.com/reference.png"}},
		},
		StorageOptions: &xaiapi.StorageOptions{Filename: "result.mp4", PublicUrl: &xaiapi.PublicUrlOptions{}},
	})
	if err != nil {
		t.Fatalf("GenerateVideo() error = %v", err)
	}
	if response.RequestId != "task_generate" {
		t.Fatalf("request ID = %q", response.RequestId)
	}
	generate := <-captured
	if generate.path != "/v1/videos" || generate.body["image_file_id"] != "file_image" || generate.body["aspect_ratio"] != "9:16" || generate.body["resolution"] != "720p" {
		t.Fatalf("unexpected generate bridge request: path=%s body=%+v", generate.path, generate.body)
	}

	extendDuration := int32(10)
	extend, err := client.ExtendVideo(ctx, &xaiapi.ExtendVideoRequest{
		Prompt:   "continue",
		Model:    "grok-imagine-video",
		Duration: &extendDuration,
		Video:    &xaiapi.VideoUrlContent{Source: &xaiapi.VideoUrlContent_Url{Url: "https://example.com/source.mp4"}},
	})
	if err != nil {
		t.Fatalf("ExtendVideo() error = %v", err)
	}
	if extend.RequestId != "task_extend" {
		t.Fatalf("extension request ID = %q", extend.RequestId)
	}
	extension := <-captured
	if extension.path != "/v1/videos/extensions" || extension.body["operation"] != "extend" || extension.body["video_url"] != "https://example.com/source.mp4" {
		t.Fatalf("unexpected extension bridge request: path=%s body=%+v", extension.path, extension.body)
	}
}

func TestOfficialFilesRPCLifecycle(t *testing.T) {
	oldDB := model.DB
	database, err := gorm.Open(sqlite.Open("file:xai-grpc-files?mode=memory&cache=shared"), &gorm.Config{})
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
	t.Setenv("XAI_PUBLIC_BASE_URL", "https://files.example.com")

	auditCalls := 0
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer platform-key" {
			http.Error(writer, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		auditCalls++
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"user_id":7,"token_id":11,"token_name":"sdk"}`))
	})
	connection := testGRPCConnection(t, handler)
	client := xaiapi.NewFilesClient(connection)
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer platform-key")

	upload, err := client.UploadFile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expiresAfter := int64(3600)
	fileContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	if err = upload.Send(&xaiapi.UploadFileChunk{Chunk: &xaiapi.UploadFileChunk_Init{Init: &xaiapi.UploadFileInit{Name: "input.png", ExpiresAfter: &expiresAfter}}}); err != nil {
		t.Fatal(err)
	}
	if err = upload.Send(&xaiapi.UploadFileChunk{Chunk: &xaiapi.UploadFileChunk_Data{Data: fileContent}}); err != nil {
		t.Fatal(err)
	}
	uploaded, err := upload.CloseAndRecv()
	if err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	if !strings.HasPrefix(uploaded.Id, "file_newapi_") || uploaded.Filename != "input.png" || uploaded.Size != int64(len(fileContent)) {
		t.Fatalf("unexpected uploaded file: %+v", uploaded)
	}

	filter := `name = "input.png" AND size_bytes >= 8`
	listed, err := client.ListFiles(ctx, &xaiapi.ListFilesRequest{Limit: 10, Filter: &filter})
	if err != nil || len(listed.Data) != 1 || listed.Data[0].Id != uploaded.Id {
		t.Fatalf("ListFiles() = %+v, %v", listed, err)
	}
	retrieved, err := client.RetrieveFile(ctx, &xaiapi.RetrieveFileRequest{FileId: uploaded.Id})
	if err != nil || retrieved.Filename != "input.png" {
		t.Fatalf("RetrieveFile() = %+v, %v", retrieved, err)
	}

	format := xaiapi.DownloadFormat_DOWNLOAD_FORMAT_ORIGINAL
	content, err := client.RetrieveFileContent(ctx, &xaiapi.RetrieveFileContentRequest{FileId: uploaded.Id, Format: &format})
	if err != nil {
		t.Fatal(err)
	}
	var downloaded []byte
	for {
		chunk, receiveErr := content.Recv()
		if receiveErr == io.EOF {
			break
		}
		if receiveErr != nil {
			t.Fatal(receiveErr)
		}
		downloaded = append(downloaded, chunk.Data...)
	}
	if string(downloaded) != string(fileContent) {
		t.Fatalf("downloaded content = %x", downloaded)
	}

	publicExpiry := int64(600)
	publicURL, err := client.CreatePublicUrl(ctx, &xaiapi.CreatePublicUrlRequest{FileId: uploaded.Id, ExpiresAfter: &publicExpiry})
	if err != nil || !strings.HasPrefix(publicURL.PublicUrl, "https://files.example.com/xai/files/") {
		t.Fatalf("CreatePublicUrl() = %+v, %v", publicURL, err)
	}
	publicURLAgain, err := client.CreatePublicUrl(ctx, &xaiapi.CreatePublicUrlRequest{FileId: uploaded.Id})
	if err != nil || publicURLAgain.PublicUrl != publicURL.PublicUrl {
		t.Fatalf("idempotent CreatePublicUrl() = %+v, %v", publicURLAgain, err)
	}
	revoked, err := client.RevokePublicUrl(ctx, &xaiapi.RevokePublicUrlRequest{FileId: uploaded.Id})
	if err != nil || !revoked.Revoked {
		t.Fatalf("RevokePublicUrl() = %+v, %v", revoked, err)
	}
	deleted, err := client.DeleteFile(ctx, &xaiapi.DeleteFileRequest{FileId: uploaded.Id})
	if err != nil || !deleted.Deleted {
		t.Fatalf("DeleteFile() = %+v, %v", deleted, err)
	}
	if auditCalls != 8 {
		t.Fatalf("authenticated/audited calls = %d, want 8", auditCalls)
	}
}

func TestTaskVideoResponsePreservesOfficialUsageAndUsesStoredURL(t *testing.T) {
	costTicks := int64(123456789)
	upstream := &xaiapi.GetDeferredVideoResponse{
		Status: xaiapi.DeferredStatus_DONE,
		Response: &xaiapi.VideoResponse{
			Model: "upstream-model",
			Video: &xaiapi.GeneratedVideo{Url: "https://temporary.example.com/video.mp4", Duration: 8, RespectModeration: true},
			Usage: &xaiapi.SamplingUsage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, CostInUsdTicks: &costTicks},
		},
	}
	data, err := protojson.Marshal(upstream)
	if err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		Status:     model.TaskStatusSuccess,
		Data:       data,
		Properties: model.Properties{OriginModelName: "grok-imagine-video"},
		PrivateData: model.TaskPrivateData{
			ResultURL:    "https://temporary.example.com/video.mp4",
			OssResultURL: "https://cdn.example.com/video.mp4",
			XAI:          &model.XAITaskPrivateData{StorageError: "local storage unavailable"},
		},
	}
	result := taskVideoResponse(task, nil)
	if result.Model != "grok-imagine-video" || result.Video.Url != "https://cdn.example.com/video.mp4" {
		t.Fatalf("unexpected video response: %+v", result)
	}
	if result.Usage == nil || result.Usage.GetCostInUsdTicks() != costTicks || result.Usage.TotalTokens != 8 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if result.Video.GetStorageError() != "local storage unavailable" {
		t.Fatalf("storage error = %q", result.Video.GetStorageError())
	}
}

func TestGetDeferredVideoPreservesExpiredStatus(t *testing.T) {
	oldDB := model.DB
	database, err := gorm.Open(sqlite.Open("file:xai-grpc-expired?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.DB = database
	t.Cleanup(func() { model.DB = oldDB })
	if err = database.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		TaskID:     "task_expired",
		Status:     model.TaskStatusFailure,
		FailReason: "xAI video request expired",
		PrivateData: model.TaskPrivateData{XAI: &model.XAITaskPrivateData{
			DeferredStatus: xaiapi.DeferredStatus_EXPIRED.String(),
		}},
	}
	if err = task.Insert(); err != nil {
		t.Fatal(err)
	}

	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"task_expired","status":"failed","error":{"code":"video_generation_failed","message":"xAI video request expired"}}`))
	})
	connection := testGRPCConnection(t, handler)
	response, err := xaiapi.NewVideoClient(connection).GetDeferredVideo(context.Background(), &xaiapi.GetDeferredVideoRequest{RequestId: task.TaskID})
	if err != nil {
		t.Fatalf("GetDeferredVideo() error = %v", err)
	}
	if response.Status != xaiapi.DeferredStatus_EXPIRED {
		t.Fatalf("status = %s, want EXPIRED", response.Status.String())
	}
}

func testGRPCConnection(t *testing.T, handler http.Handler) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(16 * 1024 * 1024)
	server := grpc.NewServer()
	bridge := &httpBridge{handler: handler}
	xaiapi.RegisterVideoServer(server, &videoServer{bridge: bridge})
	xaiapi.RegisterFilesServer(server, &filesServer{bridge: bridge})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}
