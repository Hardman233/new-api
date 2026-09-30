package xaigrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type httpBridge struct {
	handler http.Handler
}

type authenticatedContext struct {
	UserID    int    `json:"user_id"`
	TokenID   int    `json:"token_id"`
	TokenName string `json:"token_name"`
	Username  string `json:"username"`
	UserGroup string `json:"user_group"`
}

func (b *httpBridge) invoke(ctx context.Context, method, path string, body []byte) (*httptest.ResponseRecorder, error) {
	if b == nil || b.handler == nil {
		return nil, status.Error(codes.Internal, "HTTP bridge is not configured")
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	copyIncomingHeaders(ctx, request)
	recorder := httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, request)
	return recorder, nil
}

func (b *httpBridge) invokeJSON(ctx context.Context, method, path string, payload any, output any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return status.Errorf(codes.Internal, "encode bridge request: %v", err)
	}
	recorder, err := b.invoke(ctx, method, path, body)
	if err != nil {
		return err
	}
	if recorder.Code < 200 || recorder.Code >= 300 {
		return grpcErrorFromHTTP(recorder.Code, recorder.Body.Bytes())
	}
	if output == nil || recorder.Body.Len() == 0 {
		return nil
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), output); err != nil {
		return status.Errorf(codes.Internal, "decode bridge response: %v", err)
	}
	return nil
}

func (b *httpBridge) authenticate(ctx context.Context, service, method string, summary any) (*authenticatedContext, error) {
	payload := map[string]any{
		"service": service,
		"method":  method,
		"request": summary,
	}
	var auth authenticatedContext
	if err := b.invokeJSON(ctx, http.MethodPost, "/_xai/grpc/"+service+"/"+method, payload, &auth); err != nil {
		return nil, err
	}
	if auth.UserID <= 0 {
		return nil, status.Error(codes.Unauthenticated, "token authentication did not return a user")
	}
	return &auth, nil
}

func copyIncomingHeaders(ctx context.Context, request *http.Request) {
	md, _ := metadata.FromIncomingContext(ctx)
	copyFirstMetadata(md, request.Header, "authorization", "Authorization")
	copyFirstMetadata(md, request.Header, "x-request-id", "X-Request-Id")
	copyFirstMetadata(md, request.Header, "x-forwarded-for", "X-Forwarded-For")
	copyFirstMetadata(md, request.Header, "x-forwarded-proto", "X-Forwarded-Proto")
	copyFirstMetadata(md, request.Header, "x-forwarded-host", "X-Forwarded-Host")
	if authority := firstMetadata(md, ":authority", "authority", "x-forwarded-host"); authority != "" {
		request.Host = authority
	}
}

func copyFirstMetadata(md metadata.MD, headers http.Header, metadataKey, headerKey string) {
	if value := firstMetadata(md, metadataKey); value != "" {
		headers.Set(headerKey, value)
	}
}

func firstMetadata(md metadata.MD, keys ...string) string {
	for _, key := range keys {
		values := md.Get(key)
		if len(values) > 0 && strings.TrimSpace(values[0]) != "" {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func publicBaseURL(ctx context.Context) string {
	if configured := strings.TrimRight(strings.TrimSpace(os.Getenv("XAI_PUBLIC_BASE_URL")), "/"); configured != "" {
		return configured
	}
	md, _ := metadata.FromIncomingContext(ctx)
	host := firstMetadata(md, "x-forwarded-host", ":authority", "authority")
	if host == "" {
		return ""
	}
	scheme := firstMetadata(md, "x-forwarded-proto")
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + host
}

func grpcErrorFromHTTP(statusCode int, body []byte) error {
	code := codes.Internal
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		code = codes.InvalidArgument
	case http.StatusUnauthorized:
		code = codes.Unauthenticated
	case http.StatusForbidden:
		code = codes.PermissionDenied
	case http.StatusNotFound:
		code = codes.NotFound
	case http.StatusConflict:
		code = codes.AlreadyExists
	case http.StatusTooManyRequests:
		code = codes.ResourceExhausted
	case http.StatusGatewayTimeout:
		code = codes.DeadlineExceeded
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		code = codes.Unavailable
	}
	message := extractHTTPErrorMessage(body)
	if message == "" {
		message = fmt.Sprintf("HTTP bridge returned status %d", statusCode)
	}
	return status.Error(code, message)
}

func extractHTTPErrorMessage(body []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		if message, ok := payload["message"].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
		if errorValue, ok := payload["error"]; ok {
			switch value := errorValue.(type) {
			case string:
				return strings.TrimSpace(value)
			case map[string]any:
				if message, ok := value["message"].(string); ok {
					return strings.TrimSpace(message)
				}
			}
		}
	}
	return strings.TrimSpace(string(body))
}

func readRecorderBody(recorder *httptest.ResponseRecorder) ([]byte, error) {
	if recorder == nil || recorder.Result().Body == nil {
		return nil, nil
	}
	defer recorder.Result().Body.Close()
	return io.ReadAll(recorder.Result().Body)
}
