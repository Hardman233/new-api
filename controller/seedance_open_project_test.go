package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

func TestBuildSeedanceOpenUpstreamBodyInjectsConfiguredProjectName(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "flat body",
			body: `{"model":"doubao-seedance-2-5-260628","seedance_route":"zhuque","ProjectName":"client-value","BytedToken":"token"}`,
		},
		{
			name: "nested body",
			body: `{"model":"doubao-seedance-2-5-260628","seedance_route":"zhuque","body":{"ProjectName":"client-value","BytedToken":"token"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/seedance/open/GetVisualValidateResult", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
				SD2MaterialProjectName: "0807Oc",
			})

			body, err := buildSeedanceOpenUpstreamBody(c)
			if err != nil {
				t.Fatalf("buildSeedanceOpenUpstreamBody() error = %v", err)
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode upstream body: %v", err)
			}
			if payload["ProjectName"] != "0807Oc" {
				t.Fatalf("ProjectName = %#v, body=%s", payload["ProjectName"], body)
			}
			if payload["BytedToken"] != "token" {
				t.Fatalf("BytedToken missing, body=%s", body)
			}
		})
	}
}

func TestBuildSeedanceOpenUpstreamBodyOmitsUnconfiguredProjectName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/seedance/open/GetAsset", strings.NewReader(`{"model":"doubao-seedance-2-0-260128","seedance_route":"zhuque","Id":"asset"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	body, err := buildSeedanceOpenUpstreamBody(c)
	if err != nil {
		t.Fatalf("buildSeedanceOpenUpstreamBody() error = %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if _, exists := payload["ProjectName"]; exists {
		t.Fatalf("ProjectName leaked into unconfigured channel: %s", body)
	}
}

func TestSeedanceOpenWhiteCreateRequestParsesModels(t *testing.T) {
	req, err := seedanceOpenWhiteCreateRequestFromPayload(map[string]interface{}{
		"models":         []interface{}{"doubao-seedance-2-0-260128", "doubao-seedance-2-5-260628"},
		"source_url":     "https://example.com/person.png",
		"asset_type":     "Image",
		"seedance_route": "all",
	})
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	if len(req.Models) != 2 || req.Models[0] != "doubao-seedance-2-0-260128" || req.Models[1] != "doubao-seedance-2-5-260628" {
		t.Fatalf("models = %#v", req.Models)
	}
}

func TestSeedanceOpenWhiteCreateRequestRejectsModelAndModels(t *testing.T) {
	_, err := seedanceOpenWhiteCreateRequestFromPayload(map[string]interface{}{
		"model":      "doubao-seedance-2-0-260128",
		"models":     []interface{}{"doubao-seedance-2-5-260628"},
		"source_url": "https://example.com/person.png",
	})
	if err == nil {
		t.Fatal("model and models should be rejected")
	}
}

func TestSeedanceOpenWhiteCreateRequestRejectsNonStringModels(t *testing.T) {
	_, err := seedanceOpenWhiteCreateRequestFromPayload(map[string]interface{}{
		"models":     []interface{}{"doubao-seedance-2-0-260128", 25},
		"source_url": "https://example.com/person.png",
	})
	if err == nil {
		t.Fatal("non-string models item should be rejected")
	}
}

func TestSeedanceOpenWhiteCreateRequestRejectsEmptyModels(t *testing.T) {
	_, err := seedanceOpenWhiteCreateRequestFromPayload(map[string]interface{}{
		"models":     []interface{}{},
		"source_url": "https://example.com/person.png",
	})
	if err == nil {
		t.Fatal("empty models should be rejected")
	}
}
