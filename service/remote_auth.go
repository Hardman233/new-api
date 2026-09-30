package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

type internalAPIResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func RemoteAuthEnabled() bool {
	return common.GetEnvOrDefaultBool("REMOTE_AUTH_ENABLED", false)
}

func TryRemoteAuthLogin(ctx context.Context, username, password string) (*model.User, bool, string, error) {
	if !RemoteAuthEnabled() {
		return nil, false, "", nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("REMOTE_AUTH_BASE_URL")), "/")
	apiKey := strings.TrimSpace(os.Getenv("REMOTE_AUTH_API_KEY"))
	if baseURL == "" || apiKey == "" {
		return nil, true, "remote auth is not configured", fmt.Errorf("REMOTE_AUTH_BASE_URL or REMOTE_AUTH_API_KEY is empty")
	}

	reqBody := dto.InternalAuthVerifyRequest{
		Username: username,
		Password: password,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, true, "remote auth request build failed", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/internal/auth/verify", bytes.NewReader(body))
	if err != nil {
		return nil, true, "remote auth request build failed", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Api-Key", apiKey)

	client := &http.Client{Timeout: time.Duration(common.GetEnvOrDefault("REMOTE_AUTH_TIMEOUT_SEC", 5)) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, true, "remote auth service unavailable", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, true, "remote auth service returned an error", fmt.Errorf("remote auth status %d", resp.StatusCode)
	}

	var apiResp internalAPIResponse[dto.InternalAuthVerifyData]
	if err = json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, true, "remote auth response decode failed", err
	}
	if !apiResp.Success {
		msg := strings.TrimSpace(apiResp.Message)
		if msg == "" {
			msg = "invalid username or password"
		}
		return nil, true, msg, nil
	}

	syncRole := common.GetEnvOrDefaultBool("REMOTE_AUTH_SYNC_ROLE", false)
	role := common.RoleCommonUser
	if syncRole {
		role = apiResp.Data.Role
	}
	syncGroup := common.GetEnvOrDefaultBool("REMOTE_AUTH_SYNC_GROUP", false)
	group := strings.TrimSpace(common.GetEnvOrDefaultString("REMOTE_AUTH_DEFAULT_GROUP", "default"))
	if syncGroup && strings.TrimSpace(apiResp.Data.Group) != "" {
		group = strings.TrimSpace(apiResp.Data.Group)
	}

	user, err := model.UpsertRemoteMirrorUser(model.RemoteMirrorUserParams{
		SourceRegion: common.ServerRegionDomestic,
		SourceUserID: apiResp.Data.DomesticUserID,
		Username:     apiResp.Data.Username,
		DisplayName:  apiResp.Data.DisplayName,
		Email:        apiResp.Data.Email,
		Role:         role,
		Status:       apiResp.Data.Status,
		Group:        group,
		InitialQuota: common.GetEnvOrDefault("REMOTE_AUTH_INITIAL_QUOTA", 0),
		UpdateRole:   syncRole,
		UpdateGroup:  syncGroup,
	})
	if err != nil {
		return nil, true, "remote user mirror failed", err
	}
	return user, true, "", nil
}
