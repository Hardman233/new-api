package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestedModelsForSeedanceBatchCreate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/v1/seedance/open/:action", func(c *gin.Context) {
		request := &ModelRequest{
			Models: []string{" doubao-seedance-2-0-260128 ", "", "doubao-seedance-2-5-260628"},
		}
		got := requestedModelsForDistribution(c, request)
		if len(got) != 3 || got[0] != " doubao-seedance-2-0-260128 " {
			t.Fatalf("requestedModelsForDistribution() = %#v, want original parsed models", got)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/seedance/open/CreateAsset", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNoContent)
	}
}

func TestRequestedModelsForRegularRequestUsesSingleModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)

	got := requestedModelsForDistribution(c, &ModelRequest{
		Model:  "doubao-seedance-2-5-260628",
		Models: []string{"doubao-seedance-2-0-260128"},
	})
	if len(got) != 1 || got[0] != "doubao-seedance-2-5-260628" {
		t.Fatalf("requestedModelsForDistribution() = %#v, want only model field", got)
	}
}

func TestTrimModelNames(t *testing.T) {
	got := trimModelNames([]string{" sd2 ", "", "  ", "sd25"})
	if len(got) != 2 || got[0] != "sd2" || got[1] != "sd25" {
		t.Fatalf("trimModelNames() = %#v, want [sd2 sd25]", got)
	}
}

func TestIsSeedanceOpenWhiteBatchRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/v1/seedance/open/:action", func(c *gin.Context) {
		if !isSeedanceOpenWhiteBatchRequest(c, &ModelRequest{Models: []string{"sd20", "sd25"}}) {
			t.Fatal("models CreateAsset request should use batch open-white distribution")
		}
		if isSeedanceOpenWhiteBatchRequest(c, &ModelRequest{Model: "sd20"}) {
			t.Fatal("single-model CreateAsset request must keep normal distribution")
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/seedance/open/CreateAsset", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNoContent)
	}
}
