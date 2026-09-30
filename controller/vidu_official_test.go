package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestViduSubjectCreditsFromBody(t *testing.T) {
	got, ok := viduSubjectCreditsFromBody([]byte(`{"data":{"subject":{"credits":7}}}`), nil)
	require.True(t, ok)
	require.Equal(t, 7, got)

	got, ok = viduSubjectCreditsFromBody([]byte(`{"credits":8}`), map[string]interface{}{"credits": 6})
	require.True(t, ok)
	require.Equal(t, 6, got)

	got, ok = viduSubjectCreditsFromBody([]byte(`{"id":"subj_1"}`), nil)
	require.False(t, ok)
	require.Equal(t, viduSubjectCreateEstimatedCredits, got)
}

func TestViduSubjectBillingRequestInputUsesOriginalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/ent/v2/subjects", strings.NewReader(`{"model":"selector"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(middleware.ViduOfficialOriginalBody, []byte(`{"name":"original subject"}`))

	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{"X-Test": "1"},
	}
	input := viduSubjectBillingRequestInput(c, info)

	require.JSONEq(t, `{"name":"original subject"}`, string(input.Body))
	require.Equal(t, "1", input.Headers["X-Test"])
}

func TestViduSubjectActualQuotaScalesNonTieredCredits(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			Quota: 1000,
			OtherRatios: map[string]float64{
				"vidu_subject_estimated_credits": viduSubjectCreateEstimatedCredits,
			},
		},
	}

	got, tieredResult, err := viduSubjectActualQuota(info, 7)

	require.NoError(t, err)
	require.Nil(t, tieredResult)
	require.Equal(t, 1400, got)
}

func TestViduSubjectActualQuotaUsesTieredExprCreditsAndRequest(t *testing.T) {
	expr := `has(param("style"), "character") ? tier("character", c * 40) : tier("default", c * 20)`
	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:  "tiered_expr",
			ExprString:   expr,
			ExprHash:     billingexpr.ExprHashString(expr),
			GroupRatio:   1.5,
			QuotaPerUnit: common.QuotaPerUnit,
		},
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{"style":"character profile"}`),
		},
	}

	got, tieredResult, err := viduSubjectActualQuota(info, 5)

	require.NoError(t, err)
	require.NotNil(t, tieredResult)
	require.Equal(t, "character", tieredResult.MatchedTier)
	require.Equal(t, 150, got)
}

func TestViduSubjectBillingOtherKeepsIDsAndUsageSource(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "viduq3-turbo",
		RequestURLPath:  "/ent/v2/subjects",
		PriceData: types.PriceData{
			ModelPrice: 2,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1.25,
			},
		},
	}
	row := &model.ViduSubject{PublicSubjectID: "subject_public"}

	other := viduSubjectBillingOther(info, row, "subject_upstream", 5, true, model.BillingUsageSourceUpstream)

	require.Equal(t, "subject_create", other["vidu_mode"])
	require.Equal(t, "subject_public", other["public_subject_id"])
	require.Equal(t, "subject_upstream", other["upstream_subject_id"])
	require.Equal(t, model.BillingUsageSourceUpstream, other["usage_source"])
	require.Equal(t, "/ent/v2/subjects", other["request_path"])
}

func TestViduRewriteSubjectIDsForDeleteResponse(t *testing.T) {
	raw := viduRewriteSubjectIDs(
		[]byte(`{"id":"upstream_subject","subject_id":"upstream_subject","server_id":"upstream_subject","nested":{"id":"upstream_subject"}}`),
		map[string]string{"upstream_subject": "vidu_subj_public"},
	)

	require.JSONEq(t, `{"id":"vidu_subj_public","subject_id":"vidu_subj_public","server_id":"vidu_subj_public","nested":{"id":"vidu_subj_public"}}`, string(raw))
}

func TestViduTaskPublicResponseBodyUsesOSSVideoURL(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public",
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			ResultURL:    "https://upstream.example/video.mp4",
			OssResultURL: "https://oss.example/generated/video.mp4",
		},
	}

	raw := viduTaskPublicResponseBody(task, []byte(`{
		"id":"upstream_task",
		"state":"success",
		"creations":[{
			"url":"https://upstream.example/video.mp4",
			"cover_url":"https://upstream.example/cover.jpeg"
		}]
	}`))

	require.JSONEq(t, `{
		"id":"upstream_task",
		"task_id":"task_public",
		"state":"success",
		"creations":[{
			"url":"https://oss.example/generated/video.mp4",
			"cover_url":"https://upstream.example/cover.jpeg"
		}]
	}`, string(raw))
}

func TestViduTaskPublicResponseBodyKeepsUpstreamURLWithoutOSS(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public",
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://upstream.example/video.mp4",
		},
	}

	raw := viduTaskPublicResponseBody(task, []byte(`{
		"state":"success",
		"creations":[{"url":"https://upstream.example/video.mp4"}]
	}`))

	require.JSONEq(t, `{
		"task_id":"task_public",
		"state":"success",
		"creations":[{"url":"https://upstream.example/video.mp4"}]
	}`, string(raw))
}
