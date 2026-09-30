package controller

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestCopyVideoProxyRequestHeaders(t *testing.T) {
	source := make(http.Header)
	source.Set("Range", "bytes=10-19")
	source.Set("If-Range", `"video-etag"`)
	source.Set("If-None-Match", `"cached-etag"`)
	source.Set("If-Modified-Since", "Sun, 02 Aug 2026 09:00:00 GMT")
	source.Set("Authorization", "Bearer platform-secret")

	destination := make(http.Header)
	copyVideoProxyRequestHeaders(destination, source)

	for _, key := range videoProxyRequestHeaders {
		if destination.Get(key) != source.Get(key) {
			t.Fatalf("%s = %q, want %q", key, destination.Get(key), source.Get(key))
		}
	}
	if destination.Get("Authorization") != "" {
		t.Fatal("platform authorization header must not be forwarded")
	}
}

func TestCopyVideoProxyResponseHeaders(t *testing.T) {
	source := make(http.Header)
	source.Set("Accept-Ranges", "bytes")
	source.Set("Content-Range", "bytes 0-9/100")
	source.Set("Content-Type", "video/mp4")
	source.Set("ETag", `"video-etag"`)
	source.Set("Cache-Control", "public, max-age=86400")
	source.Set("Set-Cookie", "provider-cookie=secret")

	destination := make(http.Header)
	copyVideoProxyResponseHeaders(destination, source)

	for _, key := range []string{"Accept-Ranges", "Content-Range", "Content-Type", "ETag"} {
		if destination.Get(key) != source.Get(key) {
			t.Fatalf("%s = %q, want %q", key, destination.Get(key), source.Get(key))
		}
	}
	if destination.Get("Set-Cookie") != "" || destination.Get("Cache-Control") != "" {
		t.Fatal("provider cookie and cache policy must not be forwarded")
	}
}

func TestVideoProxyStreamingClientRemovesOverallTimeout(t *testing.T) {
	base := &http.Client{Timeout: 60 * time.Second}
	streaming := videoProxyStreamingClient(base)

	if streaming == base {
		t.Fatal("streaming client must be a clone")
	}
	if streaming.Timeout != 0 {
		t.Fatalf("streaming timeout = %s, want no overall timeout", streaming.Timeout)
	}
	if base.Timeout != 60*time.Second {
		t.Fatalf("base timeout changed to %s", base.Timeout)
	}
}

func TestEnsureVideoProxyUnsatisfiedContentRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead {
			t.Fatalf("method = %s, want HEAD", request.Method)
		}
		if request.Header.Get("Range") != "" || request.Header.Get("If-Range") != "" {
			t.Fatal("range conditions must be removed from size probe")
		}
		if request.Header.Get("Authorization") != "Bearer upstream-key" {
			t.Fatal("upstream authorization must be preserved")
		}
		writer.Header().Set("Content-Length", "100")
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer upstream-key")
	request.Header.Set("Range", "bytes=999-")
	request.Header.Set("If-Range", `"old-etag"`)
	response := &http.Response{StatusCode: http.StatusRequestedRangeNotSatisfiable, Header: make(http.Header)}

	ensureVideoProxyUnsatisfiedContentRange(server.Client(), request, response)

	if got := response.Header.Get("Content-Range"); got != "bytes */100" {
		t.Fatalf("Content-Range = %q, want %q", got, "bytes */100")
	}
}

func TestIsVideoProxyPassThroughStatus(t *testing.T) {
	tests := map[int]bool{
		http.StatusOK:                           true,
		http.StatusPartialContent:               true,
		http.StatusNotModified:                  true,
		http.StatusRequestedRangeNotSatisfiable: true,
		http.StatusNotFound:                     false,
		http.StatusInternalServerError:          false,
	}
	for status, want := range tests {
		if got := isVideoProxyPassThroughStatus(status); got != want {
			t.Fatalf("status %d pass-through = %t, want %t", status, got, want)
		}
	}
}

func TestIsClearlyNonVideoContentType(t *testing.T) {
	tests := map[string]bool{
		"video/mp4":                 false,
		"video/webm; charset=utf-8": false,
		"application/octet-stream":  false,
		"":                          false,
		"text/html; charset=utf-8":  true,
		"application/json":          true,
		"application/problem+json":  true,
	}
	for contentType, want := range tests {
		if got := isClearlyNonVideoContentType(contentType); got != want {
			t.Errorf("isClearlyNonVideoContentType(%q) = %v, want %v", contentType, got, want)
		}
	}
}

func TestWriteVideoDataURLSupportsRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	video := []byte("0123456789")
	dataURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(video)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/task/content", nil)
	context.Request.Header.Set("Range", "bytes=2-5")

	if err := writeVideoDataURL(context, dataURL); err != nil {
		t.Fatalf("writeVideoDataURL() error = %v", err)
	}
	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusPartialContent)
	}
	if recorder.Body.String() != "2345" {
		t.Fatalf("body = %q, want %q", recorder.Body.String(), "2345")
	}
	if got := recorder.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q, want %q", got, "bytes 2-5/10")
	}
	if got := recorder.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want %q", got, "bytes")
	}
	if got := recorder.Header().Get("Cache-Control"); got != videoProxyCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, videoProxyCacheControl)
	}
}

func TestWriteVideoDataURLRejectsUnsatisfiableRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dataURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("0123456789"))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/task/content", nil)
	context.Request.Header.Set("Range", "bytes=20-30")

	if err := writeVideoDataURL(context, dataURL); err != nil {
		t.Fatalf("writeVideoDataURL() error = %v", err)
	}
	if recorder.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestedRangeNotSatisfiable)
	}
	if got := recorder.Header().Get("Content-Range"); got != "bytes */10" {
		t.Fatalf("Content-Range = %q, want %q", got, "bytes */10")
	}
}

func TestWriteVideoDataURLSupportsHead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dataURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("0123456789"))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodHead, "/v1/videos/task/content", nil)

	if err := writeVideoDataURL(context, dataURL); err != nil {
		t.Fatalf("writeVideoDataURL() error = %v", err)
	}
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("HEAD response status=%d body_bytes=%d", recorder.Code, recorder.Body.Len())
	}
	if got := recorder.Header().Get("Content-Length"); got != "10" {
		t.Fatalf("Content-Length = %q, want %q", got, "10")
	}
}
