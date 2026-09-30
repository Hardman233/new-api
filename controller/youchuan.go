package controller

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	taskyouchuan "github.com/QuantumNous/new-api/relay/channel/task/youchuan"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

var youchuanTaskPlatform = constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeAliYouchuan))

func PrepareYouchuanTaskRoute(c *gin.Context) {
	var parent struct {
		JobID string `json:"jobId"`
	}
	if err := common.UnmarshalBodyReusable(c, &parent); err != nil {
		youchuanError(c, http.StatusBadRequest, "Invalid_Argument", err.Error())
		c.Abort()
		return
	}
	if strings.TrimSpace(parent.JobID) == "" {
		c.Next()
		return
	}
	task, found, err := model.FindTaskByUpstreamID(youchuanTaskPlatform, parent.JobID, common.GetContextKeyInt(c, constant.ContextKeyUserId))
	if err != nil {
		youchuanError(c, http.StatusInternalServerError, "Internal_Server_Error", "failed to resolve parent task")
		c.Abort()
		return
	}
	if !found || task == nil {
		youchuanError(c, http.StatusNotFound, "Job_Not_Found", "jobId is not accessible")
		c.Abort()
		return
	}
	fingerprint := taskYouchuanCredentialFingerprint(task)
	if fingerprint == "" {
		youchuanError(c, http.StatusConflict, "Credential_Affinity_Unavailable", "parent task credential affinity is unavailable")
		c.Abort()
		return
	}
	if strings.TrimSpace(task.Group) != "" {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, task.Group)
	}
	c.Set("youchuan_locked_channel_id", task.ChannelId)
	c.Set("youchuan_locked_credential_fingerprint", fingerprint)
	c.Set("youchuan_parent_upstream_task_id", parent.JobID)
	c.Next()
}

func YouchuanJob(c *gin.Context) {
	jobID := strings.TrimSpace(c.Param("jobId"))
	if jobID == "" {
		youchuanError(c, http.StatusBadRequest, "Invalid_Argument", "jobId is required")
		return
	}
	task, found, err := model.FindTaskByUpstreamID(youchuanTaskPlatform, jobID, common.GetContextKeyInt(c, constant.ContextKeyUserId))
	if err != nil {
		youchuanError(c, http.StatusInternalServerError, "Internal_Server_Error", "failed to load task")
		return
	}
	if !found || task == nil {
		youchuanError(c, http.StatusNotFound, "Job_Not_Found", "jobId is not accessible")
		return
	}
	c.Header("X-New-Api-Task-Id", task.TaskID)
	c.Header("X-New-Api-Upstream-Task-Id", task.GetUpstreamTaskID())
	c.Header("X-New-Api-Billing-Id", task.TaskID)
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		if err := service.ProcessYouchuanTaskPostprocess(c.Request.Context(), task); err != nil {
			youchuanError(c, http.StatusServiceUnavailable, "Task_Postprocess_Pending", "task output processing is not complete")
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", task.Data)
		return
	}
	channel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil || channel.Type != constant.ChannelTypeAliYouchuan {
		youchuanError(c, http.StatusBadGateway, "Channel_Not_Available", "task channel is unavailable")
		return
	}
	fingerprint := taskYouchuanCredentialFingerprint(task)
	if fingerprint == "" {
		youchuanError(c, http.StatusConflict, "Credential_Affinity_Unavailable", "task credential affinity is unavailable")
		return
	}
	c.Set("youchuan_locked_credential_fingerprint", fingerprint)
	proxyYouchuanRequest(c, channel)
}

func taskYouchuanCredentialFingerprint(task *model.Task) string {
	if task == nil || task.PrivateData.Youchuan == nil {
		return ""
	}
	return strings.TrimSpace(task.PrivateData.Youchuan.CredentialFingerprint)
}

func YouchuanProxy(c *gin.Context) {
	channel, err := resolveYouchuanUtilityChannel(c)
	if err != nil {
		youchuanError(c, http.StatusBadRequest, "Channel_Selection_Failed", err.Error())
		return
	}
	proxyYouchuanRequest(c, channel)
}

func YouchuanCallback(c *gin.Context) {
	if !c.GetBool("youchuan_upstream_callback") {
		YouchuanProxy(c)
		return
	}
	body, err := requestBodyBytes(c)
	if err != nil {
		youchuanError(c, http.StatusBadRequest, "Invalid_Argument", "invalid callback body")
		return
	}
	var response taskyouchuan.TaskResponse
	if err = json.Unmarshal(body, &response); err != nil || strings.TrimSpace(response.ID) == "" {
		youchuanError(c, http.StatusBadRequest, "Invalid_Argument", "invalid callback task response")
		return
	}
	tasks, err := model.FindTasksByUpstreamID(youchuanTaskPlatform, response.ID, 0)
	if err != nil {
		youchuanError(c, http.StatusInternalServerError, "Internal_Server_Error", "failed to load callback task")
		return
	}
	if len(tasks) == 0 {
		youchuanError(c, http.StatusNotFound, "Job_Not_Found", "callback task was not found")
		return
	}
	presentedCredential := taskyouchuan.Credential{
		AppID:  strings.TrimSpace(c.GetHeader("x-youchuan-app")),
		Secret: strings.TrimSpace(c.GetHeader("x-youchuan-secret")),
	}
	presentedFingerprint := taskyouchuan.CredentialFingerprint(presentedCredential)
	task := selectYouchuanCallbackTask(tasks, presentedFingerprint)
	if task == nil {
		youchuanError(c, http.StatusUnauthorized, "Invalid_App_Identifier", "invalid callback credential")
		return
	}
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		if err := processYouchuanTerminalTask(c, task); err != nil && !errors.Is(err, service.ErrYouchuanFinalizationBusy) {
			youchuanError(c, http.StatusServiceUnavailable, "Task_Postprocess_Pending", "task output processing is not complete")
			return
		}
		c.Status(http.StatusOK)
		return
	}
	result := taskyouchuan.ParseTaskResponse(response)
	oldStatus := task.Status
	task.Status = model.TaskStatus(result.Status)
	task.Progress = result.Progress
	task.FailReason = result.Reason
	task.Data = append(task.Data[:0], body...)
	if task.PrivateData.Youchuan == nil {
		task.PrivateData.Youchuan = &model.YouchuanTaskPrivateData{}
	}
	applyYouchuanResponsePrivateData(task.PrivateData.Youchuan, response)
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		task.FinishTime = time.Now().Unix()
	}
	won, err := task.UpdateWithStatus(oldStatus)
	if err != nil {
		youchuanError(c, http.StatusInternalServerError, "Internal_Server_Error", "failed to persist callback")
		return
	}
	if won {
		switch task.Status {
		case model.TaskStatusSuccess:
			service.SettleYouchuanTaskQuota(c.Request.Context(), task, "Youchuan callback")
		case model.TaskStatusFailure:
			service.RefundTaskQuota(c.Request.Context(), task, task.FailReason)
		}
		if err := processYouchuanTerminalTask(c, task); err != nil && !errors.Is(err, service.ErrYouchuanFinalizationBusy) {
			youchuanError(c, http.StatusServiceUnavailable, "Task_Postprocess_Pending", "task output processing is not complete")
			return
		}
	} else if latest, latestFound, latestErr := model.FindTaskByUpstreamID(youchuanTaskPlatform, response.ID, 0); latestErr == nil && latestFound && latest != nil &&
		(latest.Status == model.TaskStatusSuccess || latest.Status == model.TaskStatusFailure) {
		_ = processYouchuanTerminalTask(c, latest)
	}
	c.Status(http.StatusOK)
}

func selectYouchuanCallbackTask(tasks []*model.Task, presentedFingerprint string) *model.Task {
	for _, candidate := range tasks {
		if candidate != nil && taskYouchuanCredentialFingerprint(candidate) != "" &&
			constantTimeStringEqual(taskYouchuanCredentialFingerprint(candidate), presentedFingerprint) {
			return candidate
		}
	}
	return nil
}

func processYouchuanTerminalTask(c *gin.Context, task *model.Task) error {
	if task == nil {
		return nil
	}
	if task.Status == model.TaskStatusSuccess {
		service.SettleYouchuanTaskQuota(c.Request.Context(), task, "Youchuan terminal recovery")
	} else if task.Status == model.TaskStatusFailure {
		service.RefundTaskQuota(c.Request.Context(), task, task.FailReason)
	}
	return service.ProcessYouchuanTaskPostprocess(c.Request.Context(), task)
}

func resolveYouchuanUtilityChannel(c *gin.Context) (*model.Channel, error) {
	channels, err := model.GetEnabledChannelsByType(constant.ChannelTypeAliYouchuan)
	if err != nil {
		return nil, err
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	}
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	accessible := make([]*model.Channel, 0, len(channels))
	for _, channel := range channels {
		if channelHasGroup(channel, group) {
			accessible = append(accessible, channel)
		}
	}
	requestedID := strings.TrimSpace(c.GetHeader("X-New-Api-Channel-Id"))
	if requestedID != "" {
		id, parseErr := strconv.Atoi(requestedID)
		if parseErr != nil {
			return nil, errors.New("X-New-Api-Channel-Id must be an integer")
		}
		for _, channel := range accessible {
			if channel.Id == id {
				return channel, nil
			}
		}
		return nil, errors.New("requested Youchuan channel is unavailable")
	}
	if len(accessible) == 1 {
		return accessible[0], nil
	}
	if len(accessible) == 0 {
		return nil, errors.New("no accessible Youchuan channel")
	}
	return nil, errors.New("multiple Youchuan channels are available; set X-New-Api-Channel-Id")
}

func channelHasGroup(channel *model.Channel, group string) bool {
	if channel == nil {
		return false
	}
	if strings.TrimSpace(group) == "" || group == "auto" {
		return true
	}
	for _, candidate := range channel.GetGroups() {
		if candidate == group {
			return true
		}
	}
	return false
}

func constantTimeStringEqual(left, right string) bool {
	leftSum := sha256.Sum256([]byte(left))
	rightSum := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftSum[:], rightSum[:]) == 1
}

func proxyYouchuanRequest(c *gin.Context, channel *model.Channel) {
	if channel == nil {
		youchuanError(c, http.StatusBadRequest, "Channel_Selection_Failed", "Youchuan channel is required")
		return
	}
	if apiErr := middleware.SetupContextForSelectedChannel(c, channel, "youchuan-account"); apiErr != nil {
		youchuanError(c, http.StatusBadGateway, "Channel_Not_Available", apiErr.Error())
		return
	}
	credential, err := taskyouchuan.ParseCredential(common.GetContextKeyString(c, constant.ContextKeyChannelKey))
	if err != nil {
		youchuanError(c, http.StatusBadGateway, "Invalid_App_Identifier", "selected channel credential is invalid")
		return
	}
	body, err := requestBodyBytes(c)
	if err != nil {
		youchuanError(c, http.StatusBadRequest, "Invalid_Argument", "invalid request body")
		return
	}
	baseURL := strings.TrimRight(channel.GetBaseURL(), "/")
	if baseURL == "" {
		baseURL = taskyouchuan.DefaultBaseURL
	}
	upstreamURL := baseURL + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		upstreamURL += "?" + c.Request.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		youchuanError(c, http.StatusInternalServerError, "Internal_Server_Error", "failed to build upstream request")
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-youchuan-app", credential.AppID)
	req.Header.Set("x-youchuan-secret", credential.Secret)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	client, err := service.GetHttpClientWithProxy(channel.GetSetting().Proxy)
	if err != nil {
		youchuanError(c, http.StatusBadGateway, "Upstream_Request_Failed", "failed to create upstream client")
		return
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := taskyouchuan.CloneNoRedirectClient(client).Do(req)
	if err != nil {
		youchuanError(c, http.StatusBadGateway, "Upstream_Request_Failed", err.Error())
		return
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		youchuanError(c, http.StatusBadGateway, "Upstream_Response_Failed", "failed to read upstream response")
		return
	}
	for key, values := range resp.Header {
		if len(values) > 0 && service.ShouldCopyUpstreamHeader(c, key, values) {
			c.Header(key, values[0])
		}
	}
	c.Header("X-New-Api-Upstream-Channel-Id", strconv.Itoa(channel.Id))
	c.Header("X-New-Api-Upstream-Channel-Name", channel.Name)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Data(resp.StatusCode, contentType, responseBody)
}

func requestBodyBytes(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil, nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	return storage.Bytes()
}

func applyYouchuanResponsePrivateData(private *model.YouchuanTaskPrivateData, response taskyouchuan.TaskResponse) {
	private.Seed = response.Seed
	private.RawCost, _ = json.Marshal(response.Cost)
	parsed := taskyouchuan.ParseTaskResponse(response)
	private.Outputs = make([]model.YouchuanTaskOutput, 0, len(parsed.Outputs))
	for _, output := range parsed.Outputs {
		private.Outputs = append(private.Outputs, model.YouchuanTaskOutput{
			Index: output.Index, RemoteURL: output.RemoteURL, MediaKind: output.MediaKind, Audit: output.Audit,
		})
	}
}

func youchuanError(c *gin.Context, status int, reason, message string) {
	c.JSON(status, taskyouchuan.ErrorResponse{Code: status, Reason: reason, Message: message})
}
