package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
)

func TestViduRequestConvertOfficialCreate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ViduRequestConvert())
	router.POST("/ent/v2/text2video", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer sk-test" {
			t.Fatalf("authorization = %q", c.GetHeader("Authorization"))
		}
		if !c.GetBool("vidu_official_api") || !c.GetBool(ViduOfficialCompatKey) {
			t.Fatal("official compat flags were not set")
		}
		if c.GetString("platform") != ViduOfficialPlatformString {
			t.Fatalf("platform = %q", c.GetString("platform"))
		}
		if c.GetInt("relay_mode") != relayconstant.RelayModeVideoSubmit {
			t.Fatalf("relay_mode = %d", c.GetInt("relay_mode"))
		}
		var payload map[string]interface{}
		if err := common.UnmarshalBodyReusable(c, &payload); err != nil {
			t.Fatalf("UnmarshalBodyReusable() error = %v", err)
		}
		if payload["model"] != "viduq3-pro" || payload["mode"] != "text2video" || payload["prompt"] != "hello" {
			t.Fatalf("payload = %#v", payload)
		}
		metadata, ok := payload["metadata"].(map[string]interface{})
		if !ok || metadata["resolution"] != "720p" {
			t.Fatalf("metadata = %#v", payload["metadata"])
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/ent/v2/text2video", strings.NewReader(`{"model":"viduq3-pro","prompt":"hello","resolution":"720p"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token sk-test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestViduRequestConvertOfficialFetch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ViduRequestConvert())
	router.GET("/ent/v2/tasks/:task_id/creations", func(c *gin.Context) {
		if c.GetString("task_id") != "task_public" {
			t.Fatalf("task_id = %q", c.GetString("task_id"))
		}
		if c.GetInt("relay_mode") != relayconstant.RelayModeVideoFetchByID {
			t.Fatalf("relay_mode = %d", c.GetInt("relay_mode"))
		}
		if c.GetString("platform") != ViduOfficialPlatformString {
			t.Fatalf("platform = %q", c.GetString("platform"))
		}
		c.Status(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ent/v2/tasks/task_public/creations", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestViduProxyRequestPrepareKeepsOriginalBodyForProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ViduProxyRequestPrepare())
	router.POST("/tools/v2/files/uploads", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer sk-test" {
			t.Fatalf("authorization = %q", c.GetHeader("Authorization"))
		}
		raw, _ := c.Get(ViduOfficialOriginalBody)
		if string(raw.([]byte)) != `{"filename":"a.png"}` {
			t.Fatalf("original body = %s", raw)
		}
		var selectPayload map[string]interface{}
		if err := common.UnmarshalBodyReusable(c, &selectPayload); err != nil {
			t.Fatalf("UnmarshalBodyReusable() error = %v", err)
		}
		if selectPayload["model"] != ViduOfficialDefaultModel {
			b, _ := json.Marshal(selectPayload)
			t.Fatalf("selection payload = %s", b)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/tools/v2/files/uploads", strings.NewReader(`{"filename":"a.png"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token sk-test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
