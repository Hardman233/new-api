package xaigrpc

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/internal/xaiapi"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/xaifile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const fileContentChunkSize = 256 * 1024

type filesServer struct {
	xaiapi.UnimplementedFilesServer
	bridge *httpBridge
}

func (s *filesServer) UploadFile(stream grpc.ClientStreamingServer[xaiapi.UploadFileChunk, xaiapi.File]) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "upload initialization is required: %v", err)
	}
	init := first.GetInit()
	if init == nil || strings.TrimSpace(init.Name) == "" {
		return status.Error(codes.InvalidArgument, "the first upload chunk must contain a filename")
	}
	auth, err := s.bridge.authenticate(stream.Context(), "Files", "UploadFile", map[string]any{
		"filename": init.Name, "expires_after": init.ExpiresAfter,
	})
	if err != nil {
		return err
	}
	reader := &uploadStreamReader{stream: stream}
	file, err := xaifile.Store(stream.Context(), auth.UserID, auth.TokenID, init.Name, init.ExpiresAfter, reader)
	if err != nil {
		return fileServiceError(err)
	}
	return stream.SendAndClose(fileProto(file))
}

func (s *filesServer) ListFiles(ctx context.Context, request *xaiapi.ListFilesRequest) (*xaiapi.ListFilesResponse, error) {
	if request == nil {
		request = &xaiapi.ListFilesRequest{}
	}
	auth, err := s.bridge.authenticate(ctx, "Files", "ListFiles", request)
	if err != nil {
		return nil, err
	}
	offset, err := model.DecodeXAIFilePaginationOffset(request.GetPaginationToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid pagination_token")
	}
	limit := int(request.GetLimit())
	if limit <= 0 {
		limit = 100
	}
	files, hasMore, err := model.ListXAIFiles(model.XAIFileListParams{
		UserID:     auth.UserID,
		Limit:      limit,
		Offset:     offset,
		Descending: request.GetOrder() == xaiapi.Ordering_DESCENDING,
		SortBy:     filesSortName(request.GetSortBy()),
		Filter:     request.GetFilter(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list files: %v", err)
	}
	response := &xaiapi.ListFilesResponse{Data: make([]*xaiapi.File, 0, len(files))}
	for _, file := range files {
		response.Data = append(response.Data, fileProto(file))
	}
	if hasMore {
		token := model.EncodeXAIFilePaginationOffset(offset + len(files))
		response.PaginationToken = &token
	}
	return response, nil
}

func (s *filesServer) RetrieveFile(ctx context.Context, request *xaiapi.RetrieveFileRequest) (*xaiapi.File, error) {
	if request == nil || strings.TrimSpace(request.FileId) == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}
	auth, err := s.bridge.authenticate(ctx, "Files", "RetrieveFile", request)
	if err != nil {
		return nil, err
	}
	file, err := model.GetXAIFileForUser(auth.UserID, request.FileId)
	if err != nil {
		return nil, fileServiceError(err)
	}
	return fileProto(file), nil
}

func (s *filesServer) DeleteFile(ctx context.Context, request *xaiapi.DeleteFileRequest) (*xaiapi.DeleteFileResponse, error) {
	if request == nil || strings.TrimSpace(request.FileId) == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}
	auth, err := s.bridge.authenticate(ctx, "Files", "DeleteFile", request)
	if err != nil {
		return nil, err
	}
	file, err := xaifile.Delete(ctx, auth.UserID, request.FileId)
	if err != nil {
		return nil, fileServiceError(err)
	}
	return &xaiapi.DeleteFileResponse{Id: file.FileID, Deleted: true}, nil
}

func (s *filesServer) RetrieveFileContent(request *xaiapi.RetrieveFileContentRequest, stream grpc.ServerStreamingServer[xaiapi.FileContentChunk]) error {
	if request == nil || strings.TrimSpace(request.FileId) == "" {
		return status.Error(codes.InvalidArgument, "file_id is required")
	}
	auth, err := s.bridge.authenticate(stream.Context(), "Files", "RetrieveFileContent", request)
	if err != nil {
		return err
	}
	var data []byte
	if request.GetFormat() == xaiapi.DownloadFormat_DOWNLOAD_FORMAT_TEXT {
		_, data, err = xaifile.TextContentForUser(stream.Context(), auth.UserID, request.FileId)
	} else {
		_, data, err = xaifile.ReadForUser(stream.Context(), auth.UserID, request.FileId)
	}
	if err != nil {
		return fileServiceError(err)
	}
	for offset := 0; offset < len(data); offset += fileContentChunkSize {
		end := offset + fileContentChunkSize
		if end > len(data) {
			end = len(data)
		}
		if err = stream.Send(&xaiapi.FileContentChunk{Data: data[offset:end]}); err != nil {
			return err
		}
	}
	return nil
}

func (s *filesServer) CreatePublicUrl(ctx context.Context, request *xaiapi.CreatePublicUrlRequest) (*xaiapi.CreatePublicUrlResponse, error) {
	if request == nil || strings.TrimSpace(request.FileId) == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}
	auth, err := s.bridge.authenticate(ctx, "Files", "CreatePublicUrl", request)
	if err != nil {
		return nil, err
	}
	file, err := xaifile.CreatePublicURL(auth.UserID, request.FileId, publicBaseURL(ctx), request.ExpiresAfter)
	if err != nil {
		return nil, fileServiceError(err)
	}
	response := &xaiapi.CreatePublicUrlResponse{PublicUrl: file.PublicURL}
	if file.PublicURLExpiresAt > 0 {
		response.ExpiresAt = timestamppb.New(time.Unix(file.PublicURLExpiresAt, 0))
	}
	return response, nil
}

func (s *filesServer) RevokePublicUrl(ctx context.Context, request *xaiapi.RevokePublicUrlRequest) (*xaiapi.RevokePublicUrlResponse, error) {
	if request == nil || strings.TrimSpace(request.FileId) == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}
	auth, err := s.bridge.authenticate(ctx, "Files", "RevokePublicUrl", request)
	if err != nil {
		return nil, err
	}
	file, previous, err := model.RevokeXAIFilePublicURL(auth.UserID, request.FileId)
	if err != nil {
		return nil, fileServiceError(err)
	}
	response := &xaiapi.RevokePublicUrlResponse{FileId: file.FileID, Revoked: previous != ""}
	if previous != "" {
		response.PublicUrl = &previous
	}
	return response, nil
}

type uploadStreamReader struct {
	stream  grpc.ClientStreamingServer[xaiapi.UploadFileChunk, xaiapi.File]
	pending []byte
}

func (r *uploadStreamReader) Read(buffer []byte) (int, error) {
	for len(r.pending) == 0 {
		chunk, err := r.stream.Recv()
		if err != nil {
			return 0, err
		}
		if chunk.GetInit() != nil {
			return 0, status.Error(codes.InvalidArgument, "upload initialization may only be sent once")
		}
		r.pending = chunk.GetData()
	}
	count := copy(buffer, r.pending)
	r.pending = r.pending[count:]
	return count, nil
}

func fileProto(file *model.XAIFile) *xaiapi.File {
	if file == nil {
		return nil
	}
	result := &xaiapi.File{
		Id:        file.FileID,
		Filename:  file.Filename,
		Size:      file.SizeBytes,
		CreatedAt: timestamppb.New(time.Unix(file.CreatedAt, 0)),
	}
	if file.ExpiresAt > 0 {
		result.ExpiresAt = timestamppb.New(time.Unix(file.ExpiresAt, 0))
	}
	if file.PublicURL != "" {
		publicURL := file.PublicURL
		result.PublicUrl = &publicURL
		if file.PublicURLExpiresAt > 0 {
			result.PublicUrlExpiresAt = timestamppb.New(time.Unix(file.PublicURLExpiresAt, 0))
		}
	}
	return result
}

func filesSortName(value xaiapi.FilesSortBy) string {
	switch value {
	case xaiapi.FilesSortBy_FILES_SORT_BY_FILENAME:
		return "filename"
	case xaiapi.FilesSortBy_FILES_SORT_BY_SIZE:
		return "size"
	default:
		return "created_at"
	}
}

func fileServiceError(err error) error {
	switch {
	case errors.Is(err, model.ErrXAIFileNotFound), errors.Is(err, model.ErrXAIFileExpired):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, model.ErrXAIFileInvalidFilter):
		return status.Error(codes.InvalidArgument, err.Error())
	case status.Code(err) != codes.Unknown:
		return err
	default:
		return status.Error(codes.InvalidArgument, err.Error())
	}
}

var _ io.Reader = (*uploadStreamReader)(nil)
