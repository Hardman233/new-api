package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	vidutask "github.com/QuantumNous/new-api/relay/channel/task/vidu"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayhelper "github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const viduTaskPlatform = constant.TaskPlatform("52")
const viduSubjectCreateEstimatedCredits = 5

func ViduOfficialTaskList(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userID := c.GetInt("id")

	queryParams := model.SyncTaskQueryParams{
		Platform: viduTaskPlatform,
		TaskID:   strings.TrimSpace(c.Query("task_id")),
		Status:   viduInternalTaskStatus(c.Query("status")),
		Action:   viduInternalTaskAction(c.Query("action")),
	}
	items := model.TaskGetAllUserTask(userID, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := model.TaskCountAllUserTask(userID, queryParams)

	tasks := make([]map[string]interface{}, 0, len(items))
	for _, task := range items {
		tasks = append(tasks, viduOfficialTaskMap(task))
	}
	c.JSON(http.StatusOK, gin.H{
		"tasks":     tasks,
		"total":     total,
		"page":      pageInfo.Page,
		"page_size": pageInfo.PageSize,
	})
}

func ViduOfficialCreations(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	if taskID == "" {
		viduOfficialError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "task_id is required")
		return
	}

	task, exists, err := model.GetByTaskId(c.GetInt("id"), taskID)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeQueryDataError, err.Error())
		return
	}
	if !exists {
		viduOfficialError(c, http.StatusNotFound, types.ErrorCodeInvalidRequest, "task_not_exist")
		return
	}
	if task.Platform != viduTaskPlatform {
		viduOfficialError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "task is not a Vidu task")
		return
	}

	ch, key, baseURL, proxy, err := viduChannelForTask(task)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeGetChannelFailed, err.Error())
		return
	}

	upstreamURL := fmt.Sprintf("%s/ent/v2/tasks/%s/creations", strings.TrimRight(baseURL, "/"), url.PathEscape(task.GetUpstreamTaskID()))
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeDoRequestFailed, err.Error())
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeDoRequestFailed, err.Error())
		return
	}
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		viduOfficialError(c, http.StatusBadGateway, types.ErrorCodeDoRequestFailed, err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		viduOfficialError(c, http.StatusBadGateway, types.ErrorCodeReadResponseBodyFailed, err.Error())
		return
	}
	service.RecordUpstreamResponseLog(c, nil, resp, body)
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		viduRefreshTaskFromUpstream(c, task, body)
		if task.Status == model.TaskStatusSuccess {
			if _, err := service.EnsureTaskVideoOSSURL(service.GeneratedMediaRequestContext(c), task, nil); err != nil {
				viduOfficialError(c, http.StatusBadGateway, types.ErrorCodeDoRequestFailed, "generated media upload failed: "+err.Error())
				return
			}
		}
	}

	out := viduTaskPublicResponseBody(task, body)
	contentType := resp.Header.Get("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("X-New-Api-Upstream-Channel-Id", fmt.Sprint(ch.Id))
	c.Header("X-New-Api-Upstream-Channel-Name", ch.Name)
	c.Data(resp.StatusCode, contentType, out)
}

func ViduOfficialCancel(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	if taskID == "" {
		viduOfficialError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "task_id is required")
		return
	}

	task, exists, err := model.GetByTaskId(c.GetInt("id"), taskID)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeInvalidRequest, err.Error())
		return
	}
	if !exists {
		viduOfficialError(c, http.StatusNotFound, types.ErrorCodeInvalidRequest, "task_not_exist")
		return
	}
	if task.Platform != viduTaskPlatform {
		viduOfficialError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "task is not a Vidu task")
		return
	}

	ch, key, baseURL, proxy, err := viduChannelForTask(task)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeGetChannelFailed, err.Error())
		return
	}

	upstreamURL := fmt.Sprintf("%s/ent/v2/tasks/%s/cancel", strings.TrimRight(baseURL, "/"), url.PathEscape(task.GetUpstreamTaskID()))
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamURL, nil)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeDoRequestFailed, err.Error())
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeDoRequestFailed, err.Error())
		return
	}
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		viduOfficialError(c, http.StatusBadGateway, types.ErrorCodeDoRequestFailed, err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		viduOfficialError(c, http.StatusBadGateway, types.ErrorCodeReadResponseBodyFailed, err.Error())
		return
	}
	service.RecordUpstreamResponseLog(c, nil, resp, body)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		viduForwardResponse(c, resp, body)
		return
	}

	if !viduTaskStatusFinal(task.Status) {
		fromStatus := task.Status
		now := common.GetTimestamp()
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		task.FailReason = "canceled by user"
		task.FinishTime = now
		task.UpdatedAt = now
		if len(body) > 0 && strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
			task.Data = append([]byte(nil), body...)
		}
		won, updateErr := task.UpdateWithStatus(fromStatus)
		if updateErr != nil {
			viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeInvalidRequest, updateErr.Error())
			return
		}
		if won {
			service.RefundTaskQuota(c.Request.Context(), task, "canceled by user")
		}
	}

	out := viduCancelResponseBody(task, body)
	contentType := resp.Header.Get("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("X-New-Api-Upstream-Channel-Id", fmt.Sprint(ch.Id))
	c.Header("X-New-Api-Upstream-Channel-Name", ch.Name)
	c.Data(resp.StatusCode, contentType, out)
}

func ViduOfficialCreateSubject(c *gin.Context) {
	billingInfo, billingOK := viduPrepareSubjectCreateBilling(c)
	if !billingOK {
		return
	}

	result, ok := viduForwardSelectedChannel(c)
	if !ok {
		viduFailSubjectCreateBilling(c, billingInfo, "forward vidu subject create failed")
		return
	}
	if result.resp.StatusCode < http.StatusOK || result.resp.StatusCode >= http.StatusMultipleChoices {
		viduFailSubjectCreateBilling(c, billingInfo, fmt.Sprintf("upstream status_code=%d", result.resp.StatusCode))
		viduWriteForwardedResponse(c, result)
		return
	}
	if billingInfo != nil {
		billingInfo.SetFirstResponseTime()
	}

	subjectPayload := viduSubjectPayloadFromBody(result.body)
	upstreamID := firstNonEmptyString(
		stringFromMap(subjectPayload, "id"),
		stringFromMap(subjectPayload, "subject_id"),
		stringFromMap(subjectPayload, "server_id"),
	)
	actualCredits, hasCredits := viduSubjectCreditsFromBody(result.body, subjectPayload)
	if upstreamID == "" {
		if settleErr := viduSettleSubjectCreateBilling(c, billingInfo, nil, "", actualCredits, hasCredits); settleErr != nil {
			common.SysError("settle vidu subject billing error: " + settleErr.Error())
		}
		viduWriteForwardedResponse(c, result)
		return
	}
	row, err := model.UpsertViduSubject(model.ViduSubjectUpsertParams{
		UpstreamSubjectID: upstreamID,
		Ownership:         model.ViduSubjectOwnershipPrivate,
		UserID:            c.GetInt("id"),
		Username:          c.GetString("username"),
		TokenID:           c.GetInt("token_id"),
		TokenName:         c.GetString("token_name"),
		ChannelID:         result.channelID,
		ChannelName:       result.channelName,
		Name:              stringFromMap(subjectPayload, "name"),
		Images:            subjectPayload["images"],
		Videos:            subjectPayload["videos"],
		VoiceID:           stringFromMap(subjectPayload, "voice_id"),
		Style:             stringFromMap(subjectPayload, "style"),
		Description:       stringFromMap(subjectPayload, "description"),
		Credits:           intFromMap(subjectPayload, "credits"),
		Status:            stringFromMap(subjectPayload, "status"),
		LastUpstreamRaw:   string(result.body),
	})
	if err != nil {
		if settleErr := viduSettleSubjectCreateBilling(c, billingInfo, nil, upstreamID, actualCredits, hasCredits); settleErr != nil {
			common.SysError("settle vidu subject billing error: " + settleErr.Error())
		}
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeUpdateDataError, err.Error())
		return
	}
	if row == nil {
		if settleErr := viduSettleSubjectCreateBilling(c, billingInfo, nil, upstreamID, actualCredits, hasCredits); settleErr != nil {
			common.SysError("settle vidu subject billing error: " + settleErr.Error())
		}
		viduWriteForwardedResponse(c, result)
		return
	}
	if settleErr := viduSettleSubjectCreateBilling(c, billingInfo, row, upstreamID, actualCredits, hasCredits); settleErr != nil {
		common.SysError("settle vidu subject billing error: " + settleErr.Error())
	}
	result.body = viduRewriteSubjectIDs(result.body, map[string]string{upstreamID: row.PublicSubjectID})
	viduWriteForwardedResponse(c, result)
}

func ViduOfficialListSubjects(c *gin.Context) {
	ownership := strings.ToLower(strings.TrimSpace(c.Query("ownership")))
	if ownership == "" || ownership == model.ViduSubjectOwnershipPrivate {
		limit := viduQueryInt(c.Query("count"), 20, 1, 100)
		offset := viduQueryInt(c.Query("next_page_token"), 0, 0, 1_000_000_000)
		rows, total, err := model.ListViduSubjects(c.GetInt("id"), offset, limit, splitComma(c.Query("subject_ids")))
		if err != nil {
			viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeQueryDataError, err.Error())
			return
		}
		subjects := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			subjects = append(subjects, viduSubjectPublicMap(row))
		}
		nextPageToken := ""
		if int64(offset+len(rows)) < total {
			nextPageToken = strconv.Itoa(offset + len(rows))
		}
		c.JSON(http.StatusOK, gin.H{
			"subjects":        subjects,
			"count":           len(subjects),
			"next_page_token": nextPageToken,
		})
		return
	}
	ViduOfficialProxy(c)
}

func ViduOfficialUpdateSubject(c *gin.Context) {
	publicID := strings.TrimSpace(c.Param("subject_id"))
	if publicID == "" {
		viduOfficialError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "subject_id is required")
		return
	}
	subject, exists, err := model.GetViduSubjectByPublicID(c.GetInt("id"), publicID)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeQueryDataError, err.Error())
		return
	}
	if !exists || subject == nil {
		viduOfficialError(c, http.StatusNotFound, types.ErrorCodeInvalidRequest, "subject_not_exist")
		return
	}
	ch, key, baseURL, proxy, err := viduChannelForSubject(subject)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeGetChannelFailed, err.Error())
		return
	}
	body, _ := c.Get(middleware.ViduOfficialOriginalBody)
	bodyBytes, _ := body.([]byte)
	result, ok := viduDoForward(c, viduForwardParams{
		method:      http.MethodPut,
		path:        "/ent/v2/subjects/" + url.PathEscape(subject.UpstreamSubjectID),
		query:       c.GetString(middleware.ViduOfficialOriginalQuery),
		body:        bodyBytes,
		contentType: c.GetString(middleware.ViduOfficialContentType),
		channelID:   ch.Id,
		channelName: ch.Name,
		key:         key,
		baseURL:     baseURL,
		proxy:       proxy,
	})
	if !ok {
		return
	}
	if result.resp.StatusCode >= http.StatusOK && result.resp.StatusCode < http.StatusMultipleChoices {
		subjectPayload := viduSubjectPayloadFromBody(result.body)
		_, err := model.UpsertViduSubject(model.ViduSubjectUpsertParams{
			PublicSubjectID:   subject.PublicSubjectID,
			UpstreamSubjectID: subject.UpstreamSubjectID,
			Ownership:         subject.Ownership,
			UserID:            subject.UserID,
			Username:          c.GetString("username"),
			TokenID:           c.GetInt("token_id"),
			TokenName:         c.GetString("token_name"),
			ChannelID:         subject.ChannelID,
			ChannelName:       ch.Name,
			Name:              firstNonEmptyString(stringFromMap(subjectPayload, "name"), subject.Name),
			Images:            firstNonNil(subjectPayload["images"], parseJSONValue(subject.ImagesJSON)),
			Videos:            firstNonNil(subjectPayload["videos"], parseJSONValue(subject.VideosJSON)),
			VoiceID:           firstNonEmptyString(stringFromMap(subjectPayload, "voice_id"), subject.VoiceID),
			Style:             firstNonEmptyString(stringFromMap(subjectPayload, "style"), subject.Style),
			Description:       firstNonEmptyString(stringFromMap(subjectPayload, "description"), subject.Description),
			Credits:           firstNonZeroInt(intFromMap(subjectPayload, "credits"), subject.Credits),
			Status:            firstNonEmptyString(stringFromMap(subjectPayload, "status"), subject.Status),
			LastUpstreamRaw:   string(result.body),
		})
		if err != nil {
			viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeUpdateDataError, err.Error())
			return
		}
		result.body = viduRewriteSubjectIDs(result.body, map[string]string{subject.UpstreamSubjectID: subject.PublicSubjectID})
	}
	viduWriteForwardedResponse(c, result)
}

func ViduOfficialDeleteSubject(c *gin.Context) {
	publicID := strings.TrimSpace(c.Param("subject_id"))
	if publicID == "" {
		viduOfficialError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "subject_id is required")
		return
	}
	subject, exists, err := model.GetViduSubjectByPublicID(c.GetInt("id"), publicID)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeQueryDataError, err.Error())
		return
	}
	if !exists || subject == nil {
		viduOfficialError(c, http.StatusNotFound, types.ErrorCodeInvalidRequest, "subject_not_exist")
		return
	}
	ch, key, baseURL, proxy, err := viduChannelForSubject(subject)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeGetChannelFailed, err.Error())
		return
	}

	body, _ := c.Get(middleware.ViduOfficialOriginalBody)
	bodyBytes, _ := body.([]byte)
	result, ok := viduDoForward(c, viduForwardParams{
		method:      http.MethodDelete,
		path:        "/ent/v2/subjects/" + url.PathEscape(subject.UpstreamSubjectID),
		query:       c.GetString(middleware.ViduOfficialOriginalQuery),
		body:        bodyBytes,
		contentType: c.GetString(middleware.ViduOfficialContentType),
		channelID:   ch.Id,
		channelName: ch.Name,
		key:         key,
		baseURL:     baseURL,
		proxy:       proxy,
	})
	if !ok {
		return
	}
	if result.resp.StatusCode >= http.StatusOK && result.resp.StatusCode < http.StatusMultipleChoices {
		if _, err := model.DeleteViduSubjectByPublicID(c.GetInt("id"), publicID); err != nil {
			viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeUpdateDataError, err.Error())
			return
		}
		result.body = viduRewriteSubjectIDs(result.body, map[string]string{subject.UpstreamSubjectID: subject.PublicSubjectID})
	}
	viduWriteForwardedResponse(c, result)
}

func ViduOfficialProxy(c *gin.Context) {
	result, ok := viduForwardSelectedChannel(c)
	if !ok {
		return
	}
	viduWriteForwardedResponse(c, result)
}

type viduForwardParams struct {
	method      string
	path        string
	query       string
	body        []byte
	contentType string
	channelID   int
	channelName string
	key         string
	baseURL     string
	proxy       string
}

type viduForwardResult struct {
	resp        *http.Response
	body        []byte
	contentType string
	channelID   int
	channelName string
}

func viduForwardSelectedChannel(c *gin.Context) (*viduForwardResult, bool) {
	baseURL := strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl))
	apiKey := strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyChannelKey))
	if baseURL == "" {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeGetChannelFailed, "selected channel is missing base_url")
		return nil, false
	}
	if apiKey == "" {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeGetChannelFailed, "selected channel is missing key")
		return nil, false
	}

	path := c.GetString(middleware.ViduOfficialOriginalPath)
	if path == "" {
		path = c.Request.URL.Path
	}
	body, _ := c.Get(middleware.ViduOfficialOriginalBody)
	bodyBytes, _ := body.([]byte)
	settings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)

	return viduDoForward(c, viduForwardParams{
		method:      c.Request.Method,
		path:        path,
		query:       c.GetString(middleware.ViduOfficialOriginalQuery),
		body:        bodyBytes,
		contentType: c.GetString(middleware.ViduOfficialContentType),
		channelID:   common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		channelName: common.GetContextKeyString(c, constant.ContextKeyChannelName),
		key:         apiKey,
		baseURL:     baseURL,
		proxy:       settings.Proxy,
	})
}

func viduDoForward(c *gin.Context, params viduForwardParams) (*viduForwardResult, bool) {
	upstreamURL := strings.TrimRight(params.baseURL, "/") + params.path
	if params.query != "" {
		upstreamURL += "?" + params.query
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), params.method, upstreamURL, bytes.NewReader(params.body))
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeDoRequestFailed, err.Error())
		return nil, false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+params.key)
	if strings.TrimSpace(params.contentType) != "" {
		req.Header.Set("Content-Type", params.contentType)
	} else if len(params.body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	client, err := service.GetHttpClientWithProxy(params.proxy)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeDoRequestFailed, err.Error())
		return nil, false
	}
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		viduOfficialError(c, http.StatusBadGateway, types.ErrorCodeDoRequestFailed, err.Error())
		return nil, false
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		viduOfficialError(c, http.StatusBadGateway, types.ErrorCodeReadResponseBodyFailed, err.Error())
		return nil, false
	}
	service.RecordUpstreamResponseLog(c, nil, resp, respBody)
	contentType := resp.Header.Get("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json; charset=utf-8"
	}
	return &viduForwardResult{
		resp:        resp,
		body:        respBody,
		contentType: contentType,
		channelID:   params.channelID,
		channelName: params.channelName,
	}, true
}

func viduWriteForwardedResponse(c *gin.Context, result *viduForwardResult) {
	if result == nil || result.resp == nil {
		return
	}
	c.Header("X-New-Api-Upstream-Channel-Id", fmt.Sprint(result.channelID))
	c.Header("X-New-Api-Upstream-Channel-Name", result.channelName)
	c.Data(result.resp.StatusCode, result.contentType, result.body)
}

func viduPrepareSubjectCreateBilling(c *gin.Context) (*relaycommon.RelayInfo, bool) {
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		viduOfficialError(c, http.StatusInternalServerError, types.ErrorCodeGenRelayInfoFailed, err.Error())
		return nil, false
	}
	modelName := firstNonEmptyString(c.GetString("original_model"), common.GetContextKeyString(c, constant.ContextKeyOriginalModel), middleware.ViduOfficialDefaultModel)
	info.OriginModelName = modelName
	info.InitChannelMeta(c)
	info.UpstreamModelName = modelName
	if err := relayhelper.ModelMappedHelper(c, info, nil); err != nil {
		viduOfficialError(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, err.Error())
		return nil, false
	}
	info.OriginModelName = modelName

	requestID := firstNonEmptyString(c.GetString(common.RequestIdKey), info.RequestId, common.GetTimeString()+common.GetRandomString(8))
	c.Set(common.RequestIdKey, requestID)
	info.RequestId = requestID
	c.Header(common.BillingIdKey, requestID)
	common.SetContextKey(c, constant.ContextKeyBillingId, requestID)
	common.SetContextKey(c, constant.ContextKeyBillingType, model.BillingLedgerTypeRequest)
	info.ForcePreConsume = true

	priceData, err := viduSubjectCreatePriceData(c, info, viduSubjectCreateEstimatedCredits)
	if err != nil {
		viduOfficialError(c, http.StatusBadRequest, types.ErrorCodeModelPriceError, err.Error())
		return nil, false
	}
	info.PriceData = priceData
	if priceData.FreeModel {
		return info, true
	}
	preQuota := priceData.QuotaToPreConsume
	if preQuota == 0 {
		preQuota = priceData.Quota
	}
	if apiErr := service.PreConsumeBilling(c, preQuota, info); apiErr != nil {
		statusCode := apiErr.StatusCode
		if statusCode == 0 {
			statusCode = http.StatusInternalServerError
		}
		viduOfficialError(c, statusCode, apiErr.GetErrorCode(), apiErr.Error())
		return nil, false
	}
	return info, true
}

func viduSubjectCreatePriceData(c *gin.Context, info *relaycommon.RelayInfo, credits int) (types.PriceData, error) {
	if credits <= 0 {
		credits = viduSubjectCreateEstimatedCredits
	}
	if billing_setting.GetBillingMode(info.OriginModelName) == billing_setting.BillingModeTieredExpr {
		return viduSubjectCreateTieredPriceData(c, info, credits)
	}
	priceData, err := relayhelper.ModelPriceHelperPerCall(c, info)
	if err != nil {
		return types.PriceData{}, err
	}
	priceData.Quota = billingexpr.QuotaRound(float64(priceData.Quota) * float64(credits))
	priceData.QuotaToPreConsume = priceData.Quota
	priceData.AddOtherRatio("vidu_subject_estimated_credits", float64(credits))
	info.PriceData = priceData
	return priceData, nil
}

func viduSubjectCreateTieredPriceData(c *gin.Context, info *relaycommon.RelayInfo, credits int) (types.PriceData, error) {
	exprStr, ok := billing_setting.GetBillingExpr(info.OriginModelName)
	if !ok || strings.TrimSpace(exprStr) == "" {
		return types.PriceData{}, fmt.Errorf("model %s is configured as tiered_expr but has no billing expression", info.OriginModelName)
	}
	groupRatioInfo := relayhelper.HandleGroupRatio(c, info)
	requestInput := viduSubjectBillingRequestInput(c, info)
	params := billingexpr.TokenParams{C: float64(credits)}
	rawCost, trace, err := billingexpr.RunExprWithRequest(exprStr, params, requestInput)
	if err != nil {
		return types.PriceData{}, fmt.Errorf("model %s tiered expr run failed: %w", info.OriginModelName, err)
	}
	quotaBeforeGroup := rawCost / 1_000_000 * common.QuotaPerUnit
	preQuota := billingexpr.QuotaRound(quotaBeforeGroup * groupRatioInfo.GroupRatio)
	freeModel := false
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume && groupRatioInfo.GroupRatio == 0 {
		preQuota = 0
		freeModel = true
	}
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode:               billing_setting.BillingModeTieredExpr,
		ModelName:                 info.OriginModelName,
		ExprString:                exprStr,
		ExprHash:                  billingexpr.ExprHashString(exprStr),
		GroupRatio:                groupRatioInfo.GroupRatio,
		EstimatedCompletionTokens: credits,
		EstimatedQuotaBeforeGroup: quotaBeforeGroup,
		EstimatedQuotaAfterGroup:  preQuota,
		EstimatedTier:             trace.MatchedTier,
		QuotaPerUnit:              common.QuotaPerUnit,
		ExprVersion:               billingexpr.ExprVersion(exprStr),
	}
	info.TieredBillingSnapshot = snapshot
	info.BillingRequestInput = &requestInput
	priceData := types.PriceData{
		FreeModel:         freeModel,
		GroupRatioInfo:    groupRatioInfo,
		Quota:             preQuota,
		QuotaToPreConsume: preQuota,
	}
	info.PriceData = priceData
	return priceData, nil
}

func viduSubjectBillingRequestInput(c *gin.Context, info *relaycommon.RelayInfo) billingexpr.RequestInput {
	input := billingexpr.RequestInput{
		Headers: viduCloneHeaders(nil),
	}
	if info != nil {
		input.Headers = viduCloneHeaders(info.RequestHeaders)
	}
	if raw, ok := c.Get(middleware.ViduOfficialOriginalBody); ok {
		if body, ok := raw.([]byte); ok && len(body) > 0 {
			input.Body = append([]byte(nil), body...)
			return input
		}
	}
	return input
}

func viduFailSubjectCreateBilling(c *gin.Context, info *relaycommon.RelayInfo, reason string) {
	if info == nil {
		return
	}
	if info.Billing != nil {
		info.Billing.Refund(c)
	}
	service.MarkClaimedBillingLedgerFailed(c, info, reason)
}

func viduSettleSubjectCreateBilling(c *gin.Context, info *relaycommon.RelayInfo, row *model.ViduSubject, upstreamID string, credits int, hasCredits bool) error {
	if info == nil {
		return nil
	}
	if credits <= 0 {
		credits = viduSubjectCreateEstimatedCredits
		hasCredits = false
	}
	actualQuota, tieredResult, tieredErr := viduSubjectActualQuota(info, credits)
	if tieredErr != nil {
		common.SysError("vidu subject tiered billing fallback: " + tieredErr.Error())
	}
	info.PriceData.Quota = actualQuota
	info.PriceData.QuotaToPreConsume = actualQuota

	if err := service.SettleBilling(c, info, actualQuota); err != nil {
		return err
	}
	if actualQuota != 0 {
		model.UpdateUserUsedQuotaAndRequestCount(info.UserId, actualQuota)
		model.UpdateChannelUsedQuota(info.ChannelId, actualQuota)
	}

	usageSource := model.BillingUsageSourceEstimated
	if hasCredits {
		usageSource = model.BillingUsageSourceUpstream
	}
	if actualQuota == 0 {
		usageSource = model.BillingUsageSourceNone
	}
	other := viduSubjectBillingOther(info, row, upstreamID, credits, hasCredits, usageSource)
	if info.TieredBillingSnapshot != nil {
		service.InjectTieredBillingInfo(other, info, tieredResult)
		service.InjectTieredTokenParams(other, billingexpr.TokenParams{C: float64(credits)}, nil)
		if tieredErr != nil {
			other["tiered_error"] = tieredErr.Error()
			other["billing_expr_matched"] = false
		}
	}
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId:        info.ChannelId,
		PromptTokens:     0,
		CompletionTokens: credits,
		ModelName:        info.OriginModelName,
		TokenName:        c.GetString("token_name"),
		Quota:            actualQuota,
		Content:          "Vidu subject create",
		TokenId:          info.TokenId,
		UseTimeSeconds:   int(time.Since(info.StartTime).Seconds()),
		IsStream:         false,
		Group:            info.UsingGroup,
		UsageSource:      usageSource,
		Other:            other,
	})
	return nil
}

func viduSubjectActualQuota(info *relaycommon.RelayInfo, credits int) (int, *billingexpr.TieredResult, error) {
	if info == nil {
		return 0, nil, nil
	}
	if credits <= 0 {
		credits = viduSubjectCreateEstimatedCredits
	}
	if info.TieredBillingSnapshot != nil {
		requestInput := billingexpr.RequestInput{}
		if info.BillingRequestInput != nil {
			requestInput = *info.BillingRequestInput
		}
		result, err := billingexpr.ComputeTieredQuotaWithRequest(info.TieredBillingSnapshot, billingexpr.TokenParams{C: float64(credits)}, requestInput)
		if err != nil {
			return info.TieredBillingSnapshot.EstimatedQuotaAfterGroup, nil, err
		}
		return result.ActualQuotaAfterGroup, &result, nil
	}
	estimatedCredits := viduSubjectCreateEstimatedCredits
	if info.PriceData.OtherRatios != nil {
		if v := info.PriceData.OtherRatios["vidu_subject_estimated_credits"]; v > 0 {
			estimatedCredits = int(math.Round(v))
		}
	}
	if estimatedCredits <= 0 {
		estimatedCredits = viduSubjectCreateEstimatedCredits
	}
	return billingexpr.QuotaRound(float64(info.PriceData.Quota) * float64(credits) / float64(estimatedCredits)), nil, nil
}

func viduSubjectBillingOther(info *relaycommon.RelayInfo, row *model.ViduSubject, upstreamID string, credits int, hasCredits bool, usageSource string) map[string]interface{} {
	if info == nil {
		info = &relaycommon.RelayInfo{}
	}
	other := map[string]interface{}{
		"vidu_mode":                 "subject_create",
		"estimated_credits":         viduSubjectCreateEstimatedCredits,
		"actual_credits":            credits,
		"has_upstream_credits":      hasCredits,
		"upstream_subject_id":       upstreamID,
		"usage_source":              usageSource,
		"request_path":              info.RequestURLPath,
		"model_price":               info.PriceData.ModelPrice,
		"group_ratio":               info.PriceData.GroupRatioInfo.GroupRatio,
		"vidu_subject_billing_unit": "credits",
	}
	if row != nil {
		other["public_subject_id"] = row.PublicSubjectID
	}
	if info.PriceData.ModelRatio > 0 {
		other["model_ratio"] = info.PriceData.ModelRatio
	}
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}
	upstreamModelName := ""
	if info.ChannelMeta != nil {
		upstreamModelName = info.UpstreamModelName
	}
	if upstreamModelName != "" && upstreamModelName != info.OriginModelName {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = upstreamModelName
	}
	return other
}

func viduSubjectCreditsFromBody(body []byte, subjectPayload map[string]interface{}) (int, bool) {
	if credits := intFromMap(subjectPayload, "credits"); credits > 0 {
		return credits, true
	}
	payload := map[string]interface{}{}
	if len(body) > 0 && strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		_ = common.Unmarshal(body, &payload)
	}
	for _, source := range []map[string]interface{}{payload, mapFromAny(payload["data"]), mapFromAny(payload["subject"])} {
		if credits := intFromMap(source, "credits"); credits > 0 {
			return credits, true
		}
	}
	if data := mapFromAny(payload["data"]); data != nil {
		if credits := intFromMap(mapFromAny(data["subject"]), "credits"); credits > 0 {
			return credits, true
		}
	}
	return viduSubjectCreateEstimatedCredits, false
}

func mapFromAny(value interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func viduCloneHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		dst[key] = value
	}
	return dst
}

func viduChannelForTask(task *model.Task) (*model.Channel, string, string, string, error) {
	ch, err := model.GetChannelById(task.ChannelId, true)
	if err != nil {
		return nil, "", "", "", err
	}
	if ch == nil {
		return nil, "", "", "", fmt.Errorf("channel not found")
	}
	if ch.Type != constant.ChannelTypeVidu {
		return nil, "", "", "", fmt.Errorf("task channel is not Vidu")
	}
	key, _, apiErr := ch.GetNextEnabledKey()
	if apiErr != nil {
		return nil, "", "", "", apiErr
	}
	baseURL := strings.TrimSpace(ch.GetBaseURL())
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[ch.Type]
	}
	if baseURL == "" {
		return nil, "", "", "", fmt.Errorf("channel base_url is empty")
	}
	if strings.TrimSpace(key) == "" {
		return nil, "", "", "", fmt.Errorf("channel key is empty")
	}
	return ch, strings.TrimSpace(key), baseURL, ch.GetSetting().Proxy, nil
}

func viduChannelForSubject(subject *model.ViduSubject) (*model.Channel, string, string, string, error) {
	if subject == nil {
		return nil, "", "", "", fmt.Errorf("subject is nil")
	}
	ch, err := model.GetChannelById(subject.ChannelID, true)
	if err != nil {
		return nil, "", "", "", err
	}
	if ch == nil {
		return nil, "", "", "", fmt.Errorf("channel not found")
	}
	if ch.Type != constant.ChannelTypeVidu {
		return nil, "", "", "", fmt.Errorf("subject channel is not Vidu")
	}
	key, _, apiErr := ch.GetNextEnabledKey()
	if apiErr != nil {
		return nil, "", "", "", apiErr
	}
	baseURL := strings.TrimSpace(ch.GetBaseURL())
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[ch.Type]
	}
	if baseURL == "" {
		return nil, "", "", "", fmt.Errorf("channel base_url is empty")
	}
	return ch, strings.TrimSpace(key), baseURL, ch.GetSetting().Proxy, nil
}

func viduOfficialTaskMap(task *model.Task) map[string]interface{} {
	payload := map[string]interface{}{}
	if task != nil && len(task.Data) > 0 && string(task.Data) != "null" {
		_ = common.Unmarshal(task.Data, &payload)
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["task_id"] = task.TaskID
	payload["state"] = viduOfficialState(task.Status, payload["state"])
	if modelName := viduTaskModelName(task); modelName != "" {
		payload["model"] = modelName
	}
	if task.CreatedAt > 0 && strings.TrimSpace(fmt.Sprint(payload["created_at"])) == "" {
		payload["created_at"] = time.Unix(task.CreatedAt, 0).UTC().Format(time.RFC3339)
	}
	if task.UpdatedAt > 0 && strings.TrimSpace(fmt.Sprint(payload["updated_at"])) == "" {
		payload["updated_at"] = time.Unix(task.UpdatedAt, 0).UTC().Format(time.RFC3339)
	}
	if task.Status == model.TaskStatusFailure && strings.TrimSpace(task.FailReason) != "" && strings.TrimSpace(fmt.Sprint(payload["reason"])) == "" {
		payload["reason"] = strings.TrimSpace(task.FailReason)
	}
	return payload
}

func viduCancelResponseBody(task *model.Task, upstreamBody []byte) []byte {
	payload := viduTaskPublicResponseMap(task, upstreamBody)
	if strings.TrimSpace(fmt.Sprint(payload["state"])) == "" || payload["state"] == "failed" {
		payload["state"] = "canceled"
	}
	out, _ := common.Marshal(payload)
	return out
}

func viduTaskPublicResponseBody(task *model.Task, upstreamBody []byte) []byte {
	payload := viduTaskPublicResponseMap(task, upstreamBody)
	if strings.TrimSpace(fmt.Sprint(payload["state"])) == "" {
		payload["state"] = viduOfficialState(task.Status, payload["state"])
	}
	out, _ := common.Marshal(payload)
	return out
}

func viduTaskPublicResponseMap(task *model.Task, upstreamBody []byte) map[string]interface{} {
	payload := map[string]interface{}{}
	if len(upstreamBody) > 0 && strings.HasPrefix(strings.TrimSpace(string(upstreamBody)), "{") {
		_ = common.Unmarshal(upstreamBody, &payload)
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["task_id"] = task.TaskID
	viduRewriteCreationVideoURL(payload, task)
	return payload
}

func viduRewriteCreationVideoURL(payload map[string]interface{}, task *model.Task) {
	if task == nil || task.Status != model.TaskStatusSuccess {
		return
	}
	ossURL := strings.TrimSpace(task.PrivateData.OssResultURL)
	if ossURL == "" {
		return
	}

	creations, ok := payload["creations"].([]interface{})
	if !ok {
		return
	}
	upstreamURL := strings.TrimSpace(task.PrivateData.ResultURL)
	var fallback map[string]interface{}
	var fallbackKey string
	for _, item := range creations {
		creation, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		for _, key := range []string{"url", "video_url"} {
			current := strings.TrimSpace(fmt.Sprint(creation[key]))
			if current == "" || current == "<nil>" {
				continue
			}
			if upstreamURL != "" && current == upstreamURL {
				creation[key] = ossURL
				return
			}
			if fallback == nil {
				fallback = creation
				fallbackKey = key
			}
		}
	}
	if fallback != nil {
		fallback[fallbackKey] = ossURL
	}
}

func viduOfficialState(status model.TaskStatus, existing interface{}) string {
	if status != model.TaskStatusSuccess && status != model.TaskStatusFailure {
		if state := strings.TrimSpace(fmt.Sprint(existing)); state != "" && state != "<nil>" {
			return state
		}
	}
	switch status {
	case model.TaskStatusSuccess:
		return "success"
	case model.TaskStatusFailure:
		return "failed"
	case model.TaskStatusInProgress:
		return "processing"
	case model.TaskStatusQueued:
		return "queueing"
	case model.TaskStatusSubmitted, model.TaskStatusNotStart:
		return "created"
	default:
		return "created"
	}
}

func viduTaskModelName(task *model.Task) string {
	if task == nil {
		return ""
	}
	if task.Properties.OriginModelName != "" {
		return task.Properties.OriginModelName
	}
	return task.Properties.UpstreamModelName
}

func viduTaskStatusFinal(status model.TaskStatus) bool {
	return status == model.TaskStatusSuccess || status == model.TaskStatusFailure
}

func viduForwardResponse(c *gin.Context, resp *http.Response, body []byte) {
	contentType := resp.Header.Get("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("X-New-Api-Upstream-Channel-Id", fmt.Sprint(common.GetContextKeyInt(c, constant.ContextKeyChannelId)))
	c.Header("X-New-Api-Upstream-Channel-Name", common.GetContextKeyString(c, constant.ContextKeyChannelName))
	c.Data(resp.StatusCode, contentType, body)
}

func viduRefreshTaskFromUpstream(c *gin.Context, task *model.Task, body []byte) {
	if task == nil || len(body) == 0 {
		return
	}
	taskInfo, err := (&vidutask.TaskAdaptor{}).ParseTaskResult(body)
	if err != nil || taskInfo == nil {
		return
	}

	snap := task.Snapshot()
	now := common.GetTimestamp()
	task.Data = viduTaskDataWithUsage(body, taskInfo)
	if taskInfo.Progress != "" {
		task.Progress = taskInfo.Progress
	}
	switch taskInfo.Status {
	case model.TaskStatusSuccess:
		task.Status = model.TaskStatusSuccess
		task.Progress = "100%"
		if task.StartTime == 0 {
			task.StartTime = now
		}
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.UpdatedAt = now
		if strings.TrimSpace(taskInfo.Url) != "" {
			task.PrivateData.ResultURL = taskInfo.Url
		}
	case model.TaskStatusFailure:
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.UpdatedAt = now
		task.FailReason = firstNonEmptyString(taskInfo.Reason, "task failed")
	case model.TaskStatusInProgress, model.TaskStatusQueued, model.TaskStatusSubmitted:
		task.Status = model.TaskStatus(taskInfo.Status)
		task.UpdatedAt = now
	default:
		return
	}

	wasFinal := viduTaskStatusFinal(snap.Status)
	isFinal := viduTaskStatusFinal(task.Status)
	if isFinal && !wasFinal {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil || !won {
			return
		}
		if task.Status == model.TaskStatusSuccess {
			service.EnqueueGeneratedMediaForTask(c.Request.Context(), task, taskInfo)
			viduSettleTaskBilling(c, task, taskInfo)
			return
		}
		service.RefundTaskQuota(c.Request.Context(), task, task.FailReason)
		return
	}
	if !snap.Equal(task.Snapshot()) {
		_, _ = task.UpdateWithStatus(snap.Status)
	}
}

func viduSettleTaskBilling(c *gin.Context, task *model.Task, taskInfo *relaycommon.TaskInfo) {
	if taskInfo != nil && taskInfo.TotalTokens > 0 {
		if service.RecalculateTaskQuotaByTieredTokens(c.Request.Context(), task, taskInfo.TotalTokens) {
			return
		}
		service.RecalculateTaskQuotaByTokens(c.Request.Context(), task, taskInfo.TotalTokens)
		return
	}
	service.SettleTaskQuotaExact(c.Request.Context(), task, "no billing adjustment")
}

func viduTaskDataWithUsage(body []byte, taskInfo *relaycommon.TaskInfo) []byte {
	if taskInfo == nil || taskInfo.TotalTokens <= 0 || len(body) == 0 || !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		return append([]byte(nil), body...)
	}
	var payload map[string]interface{}
	if err := common.Unmarshal(body, &payload); err != nil {
		return append([]byte(nil), body...)
	}
	usage := map[string]interface{}{
		"completion_tokens": taskInfo.TotalTokens,
		"total_tokens":      taskInfo.TotalTokens,
		"credits":           taskInfo.TotalTokens,
	}
	payload["usage"] = usage
	out, err := common.Marshal(payload)
	if err != nil {
		return append([]byte(nil), body...)
	}
	return out
}

func viduSubjectPayloadFromBody(body []byte) map[string]interface{} {
	payload := map[string]interface{}{}
	if len(body) == 0 || !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		return payload
	}
	_ = common.Unmarshal(body, &payload)
	if subject, ok := payload["subject"].(map[string]interface{}); ok {
		return subject
	}
	if data, ok := payload["data"].(map[string]interface{}); ok {
		if subject, ok := data["subject"].(map[string]interface{}); ok {
			return subject
		}
		if stringFromMap(data, "id") != "" {
			return data
		}
	}
	return payload
}

func viduSubjectPublicMap(row model.ViduSubject) map[string]interface{} {
	payload := map[string]interface{}{}
	if strings.HasPrefix(strings.TrimSpace(row.LastUpstreamRaw), "{") {
		_ = common.Unmarshal([]byte(row.LastUpstreamRaw), &payload)
		payload = viduSubjectPayloadFromBodyMust(payload)
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["id"] = row.PublicSubjectID
	payload["server_id"] = row.PublicSubjectID
	payload["name"] = firstNonEmptyString(stringFromMap(payload, "name"), row.Name)
	if images := firstNonNil(payload["images"], parseJSONValue(row.ImagesJSON)); images != nil {
		payload["images"] = images
	}
	if videos := firstNonNil(payload["videos"], parseJSONValue(row.VideosJSON)); videos != nil {
		payload["videos"] = videos
	}
	if row.VoiceID != "" {
		payload["voice_id"] = row.VoiceID
	}
	if row.Style != "" {
		payload["style"] = row.Style
	}
	if row.Description != "" {
		payload["description"] = row.Description
	}
	if row.Credits > 0 {
		payload["credits"] = row.Credits
	}
	if row.Status != "" {
		payload["status"] = row.Status
	}
	return payload
}

func viduSubjectPayloadFromBodyMust(payload map[string]interface{}) map[string]interface{} {
	if payload == nil {
		return map[string]interface{}{}
	}
	if subject, ok := payload["subject"].(map[string]interface{}); ok {
		return subject
	}
	if data, ok := payload["data"].(map[string]interface{}); ok {
		if subject, ok := data["subject"].(map[string]interface{}); ok {
			return subject
		}
		if stringFromMap(data, "id") != "" {
			return data
		}
	}
	return payload
}

func viduRewriteSubjectIDs(body []byte, idMap map[string]string) []byte {
	if len(idMap) == 0 || len(body) == 0 || !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		return body
	}
	var payload interface{}
	if err := common.Unmarshal(body, &payload); err != nil {
		return body
	}
	viduRewriteSubjectIDValue(payload, idMap)
	out, err := common.Marshal(payload)
	if err != nil {
		return body
	}
	return out
}

func viduRewriteSubjectIDValue(value interface{}, idMap map[string]string) {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, item := range v {
			if s, ok := item.(string); ok {
				if mapped, found := idMap[s]; found && (key == "id" || key == "subject_id" || key == "server_id") {
					v[key] = mapped
				}
				continue
			}
			viduRewriteSubjectIDValue(item, idMap)
		}
	case []interface{}:
		for _, item := range v {
			viduRewriteSubjectIDValue(item, idMap)
		}
	}
}

func viduInternalTaskStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "all":
		return ""
	case "created", "submitted":
		return string(model.TaskStatusSubmitted)
	case "queueing", "queued":
		return string(model.TaskStatusQueued)
	case "processing", "running":
		return string(model.TaskStatusInProgress)
	case "success", "succeeded":
		return string(model.TaskStatusSuccess)
	case "failed", "failure", "canceled", "cancelled":
		return string(model.TaskStatusFailure)
	default:
		return strings.TrimSpace(status)
	}
}

func viduInternalTaskAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", "all":
		return ""
	case "text2video", "text":
		return constant.TaskActionTextGenerate
	case "img2video", "image2video", "image":
		return constant.TaskActionGenerate
	case "start-end2video", "startend2video", "firsttail":
		return constant.TaskActionFirstTailGenerate
	case "reference2video", "reference":
		return constant.TaskActionReferenceGenerate
	default:
		return strings.TrimSpace(action)
	}
}

func splitComma(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func viduQueryInt(value string, fallback, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		n = fallback
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func stringFromMap(payload map[string]interface{}, key string) string {
	if payload == nil {
		return ""
	}
	if value, ok := payload[key]; ok && value != nil {
		if s := strings.TrimSpace(fmt.Sprint(value)); s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

func intFromMap(payload map[string]interface{}, key string) int {
	if payload == nil {
		return 0
	}
	switch v := payload[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := strconv.Atoi(v.String())
		return n
	default:
		n, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(v)))
		return n
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonNil(values ...interface{}) interface{} {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonZeroInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func parseJSONValue(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var value interface{}
	if err := common.Unmarshal([]byte(raw), &value); err != nil {
		return nil
	}
	return value
}

func viduOfficialError(c *gin.Context, statusCode int, code types.ErrorCode, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": common.MessageWithRequestId(message, c.GetString(common.RequestIdKey)),
			"type":    "new_api_error",
			"code":    string(code),
		},
	})
}
