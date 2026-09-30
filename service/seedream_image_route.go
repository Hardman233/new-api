package service

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
)

const (
	seedream5LiteModel = "doubao-seedream-5-0-260128"
	seedream5ProModel  = "doubao-seedream-5-0-pro-260628"
)

type seedream5ChannelRole uint8

const (
	seedream5ChannelBoth seedream5ChannelRole = iota
	seedream5ChannelGenerationOnly
	seedream5ChannelEditOnly
)

// Seedream5ImageChannelFilter returns a channel filter for the unified
// Seedream 5 image entry point. Wangsu's two nodes are distinguished by their
// existing base URL suffix; no channel-table schema change is required.
func Seedream5ImageChannelFilter(request *dto.ImageRequest, relayMode int) model.ChannelFilter {
	if request == nil || !isSeedream5ImageModel(request.Model) {
		return nil
	}

	needsEdit := relayMode == relayconstant.RelayModeImagesEdits || seedream5RequestHasReference(request)
	return func(channel *model.Channel) bool {
		if channel == nil {
			return false
		}
		switch classifySeedream5Channel(channel.GetBaseURL()) {
		case seedream5ChannelGenerationOnly:
			return !needsEdit
		case seedream5ChannelEditOnly:
			return needsEdit
		default:
			// Native Volcengine/Xiongda channels and unknown providers retain
			// their existing behavior because they may support both operations.
			return true
		}
	}
}

// Seedream5ImageChannelFilterFromContext parses the already reusable request
// body before the distributor selects a channel. Parse failures are left to
// the regular request validator so this routing helper cannot mask the real
// client error.
func Seedream5ImageChannelFilterFromContext(c *gin.Context, modelName string, relayMode int) model.ChannelFilter {
	if !isSeedream5ImageModel(modelName) || (relayMode != relayconstant.RelayModeImagesGenerations && relayMode != relayconstant.RelayModeImagesEdits) {
		return nil
	}

	request := &dto.ImageRequest{Model: modelName}
	if c != nil && c.Request != nil {
		if err := common.UnmarshalBodyReusable(c, request); err != nil {
			return Seedream5ImageChannelFilter(request, relayMode)
		}
	}
	if strings.TrimSpace(request.Model) == "" {
		request.Model = modelName
	}
	return Seedream5ImageChannelFilter(request, relayMode)
}

func isSeedream5ImageModel(modelName string) bool {
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case seedream5LiteModel, seedream5ProModel:
		return true
	default:
		return false
	}
}

func seedream5RequestHasReference(request *dto.ImageRequest) bool {
	return hasJSONValue(request.Image) || hasJSONValue(request.Images) || hasJSONValue(request.Mask)
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func classifySeedream5Channel(baseURL string) seedream5ChannelRole {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return seedream5ChannelBoth
	}

	path := strings.TrimRight(strings.ToLower(parsed.Path), "/")
	for _, endpoint := range []string{"/images/edits", "/images/generations"} {
		if strings.HasSuffix(path, endpoint) {
			path = strings.TrimRight(strings.TrimSuffix(path, endpoint), "/")
			break
		}
	}

	// Check edit first because both names share the lishuaikunpic prefix.
	if strings.HasSuffix(path, "/lishuaikunpicedit") {
		return seedream5ChannelEditOnly
	}
	if strings.HasSuffix(path, "/lishuaikunpic") {
		return seedream5ChannelGenerationOnly
	}
	return seedream5ChannelBoth
}
