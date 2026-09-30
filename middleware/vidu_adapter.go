package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
)

const (
	ViduOfficialCompatKey      = "vidu_official_compat"
	ViduOfficialOriginalPath   = "vidu_official_original_path"
	ViduOfficialOriginalQuery  = "vidu_official_original_query"
	ViduOfficialOriginalBody   = "vidu_official_original_body"
	ViduOfficialContentType    = "vidu_official_content_type"
	ViduOfficialDefaultModel   = "viduq3-turbo"
	ViduOfficialPlatformString = "52"
)

var viduOfficialCreateModes = map[string]string{
	"/ent/v2/text2video":      "text2video",
	"/ent/v2/img2video":       "img2video",
	"/ent/v2/start-end2video": "start-end2video",
	"/ent/v2/reference2video": "reference2video",
}

func ViduRequestConvert() func(c *gin.Context) {
	return func(c *gin.Context) {
		path := cleanViduPath(c.Request.URL.Path)
		normalizeViduAuthorization(c)
		c.Set(ViduOfficialCompatKey, true)
		c.Set("vidu_official_api", true)
		c.Set(ViduOfficialOriginalPath, path)
		c.Set(ViduOfficialOriginalQuery, c.Request.URL.RawQuery)
		c.Set("platform", ViduOfficialPlatformString)

		if c.Request.Method == http.MethodGet && strings.HasPrefix(path, "/ent/v2/tasks/") && strings.HasSuffix(path, "/creations") {
			taskID := strings.TrimSuffix(strings.TrimPrefix(path, "/ent/v2/tasks/"), "/creations")
			taskID = strings.Trim(taskID, "/")
			if taskID == "" {
				abortWithOpenAiMessage(c, http.StatusBadRequest, "task_id is required")
				return
			}
			c.Set("task_id", taskID)
			c.Set("relay_mode", relayconstant.RelayModeVideoFetchByID)
			c.Next()
			return
		}

		mode, ok := viduOfficialCreateModes[path]
		if !ok {
			c.Next()
			return
		}
		if c.Request.Method != http.MethodPost {
			abortWithOpenAiMessage(c, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		var originalReq map[string]interface{}
		if err := common.UnmarshalBodyReusable(c, &originalReq); err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		model, _ := originalReq["model"].(string)
		if model == "" {
			model, _ = originalReq["model_name"].(string)
		}
		model = strings.TrimSpace(model)
		if model == "" {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "model is required")
			return
		}

		unifiedReq := make(map[string]interface{}, len(originalReq)+3)
		for k, v := range originalReq {
			unifiedReq[k] = v
		}
		unifiedReq["model"] = model
		unifiedReq["mode"] = mode
		if prompt, _ := originalReq["prompt"].(string); prompt != "" {
			unifiedReq["prompt"] = prompt
		}
		unifiedReq["metadata"] = originalReq

		jsonData, err := json.Marshal(unifiedReq)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "failed to marshal request body")
			return
		}
		if !replaceReusableRequestBody(c, jsonData, "application/json") {
			return
		}
		c.Set("relay_mode", relayconstant.RelayModeVideoSubmit)
		c.Next()
	}
}

func ViduProxyRequestPrepare() func(c *gin.Context) {
	return func(c *gin.Context) {
		path := cleanViduPath(c.Request.URL.Path)
		normalizeViduAuthorization(c)
		body, err := readReusableBodyBytes(c)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		c.Set(ViduOfficialCompatKey, true)
		c.Set("vidu_official_api", true)
		c.Set(ViduOfficialOriginalPath, path)
		c.Set(ViduOfficialOriginalQuery, c.Request.URL.RawQuery)
		c.Set(ViduOfficialOriginalBody, body)
		c.Set(ViduOfficialContentType, c.Request.Header.Get("Content-Type"))

		modelName := strings.TrimSpace(c.Query("model"))
		if modelName == "" && len(body) > 0 && strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err == nil {
				modelName, _ = payload["model"].(string)
				if modelName == "" {
					modelName, _ = payload["model_name"].(string)
				}
			}
		}
		if strings.TrimSpace(modelName) == "" {
			modelName = ViduOfficialDefaultModel
		}

		selectBody, _ := json.Marshal(map[string]interface{}{"model": modelName})
		if !replaceReusableRequestBody(c, selectBody, "application/json") {
			return
		}
		c.Next()
	}
}

func ViduSubjectChannelBind() func(c *gin.Context) {
	return func(c *gin.Context) {
		if c == nil || c.Request == nil || !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			c.Next()
			return
		}
		body, err := readReusableBodyBytes(c)
		if err != nil || len(body) == 0 {
			c.Next()
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			c.Next()
			return
		}

		userID := c.GetInt("id")
		if userID <= 0 {
			c.Next()
			return
		}
		var boundChannelID int
		for _, subjectID := range viduSubjectServerIDs(payload) {
			subject, ok, err := model.GetViduSubjectByPublicID(userID, subjectID)
			if err != nil {
				abortWithOpenAiMessage(c, http.StatusInternalServerError, "query vidu subject failed: "+err.Error())
				return
			}
			if !ok || subject == nil || subject.ChannelID <= 0 {
				continue
			}
			if boundChannelID != 0 && boundChannelID != subject.ChannelID {
				abortWithOpenAiMessage(c, http.StatusBadRequest, "vidu subjects from different channels cannot be used in one request")
				return
			}
			boundChannelID = subject.ChannelID
		}
		if boundChannelID > 0 {
			common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(boundChannelID))
		}
		c.Next()
	}
}

func cleanViduPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(path, "/")
}

func readReusableBodyBytes(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil, nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(storage)
	return append([]byte(nil), body...), nil
}

func replaceReusableRequestBody(c *gin.Context, body []byte, contentType string) bool {
	storage, err := common.CreateBodyStorage(body)
	if err != nil {
		abortWithOpenAiMessage(c, http.StatusInternalServerError, "failed to prepare request body")
		return false
	}
	c.Set(common.KeyRequestBody, body)
	c.Set(common.KeyBodyStorage, storage)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	if contentType != "" {
		c.Request.Header.Set("Content-Type", contentType)
	}
	return true
}

func normalizeViduAuthorization(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(auth) >= len("Token ") && strings.EqualFold(auth[:len("Token ")], "Token ") {
		c.Request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(auth[len("Token "):]))
	}
}

func viduSubjectServerIDs(payload map[string]interface{}) []string {
	var ids []string
	collectSubjectIDs := func(subjects interface{}) {
		items, ok := subjects.([]interface{})
		if !ok {
			return
		}
		for _, item := range items {
			subject, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if serverID, ok := subject["server_id"].(string); ok && strings.TrimSpace(serverID) != "" {
				ids = append(ids, strings.TrimSpace(serverID))
			}
		}
	}
	collectSubjectIDs(payload["subjects"])
	if metadata, ok := payload["metadata"].(map[string]interface{}); ok {
		collectSubjectIDs(metadata["subjects"])
	}
	return ids
}
