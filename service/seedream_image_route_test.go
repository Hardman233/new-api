package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
)

func TestSeedream5ImageChannelFilterSeparatesWangsuNodes(t *testing.T) {
	genURL := "https://aigateway.edgecloudapp.com/v1/account/lishuaikunpic"
	editURL := "https://aigateway.edgecloudapp.com/v1/account/lishuaikunpicedit"

	genChannel := testSeedreamChannel(genURL)
	editChannel := testSeedreamChannel(editURL)

	imageRequest := &dto.ImageRequest{
		Model:  seedream5LiteModel,
		Prompt: "change the hair color",
		Image:  json.RawMessage(`"https://example.com/reference.png"`),
	}
	filter := Seedream5ImageChannelFilter(imageRequest, relayconstant.RelayModeImagesGenerations)
	if filter == nil {
		t.Fatal("expected reference-image filter")
	}
	if filter(genChannel) {
		t.Fatal("generation-only Wangsu channel accepted a reference-image request")
	}
	if !filter(editChannel) {
		t.Fatal("edit-only Wangsu channel rejected a reference-image request")
	}

	textRequest := &dto.ImageRequest{Model: seedream5LiteModel, Prompt: "a red bottle"}
	filter = Seedream5ImageChannelFilter(textRequest, relayconstant.RelayModeImagesGenerations)
	if !filter(genChannel) {
		t.Fatal("generation-only Wangsu channel rejected text-to-image")
	}
	if filter(editChannel) {
		t.Fatal("edit-only Wangsu channel accepted text-to-image")
	}
}

func TestSeedream5ImageChannelFilterTreatsMaskAndEditPathAsEdit(t *testing.T) {
	genChannel := testSeedreamChannel("https://aigateway.edgecloudapp.com/v1/account/lishuaikunpic")
	editChannel := testSeedreamChannel("https://aigateway.edgecloudapp.com/v1/account/lishuaikunpicedit")

	maskRequest := &dto.ImageRequest{
		Model: seedream5ProModel,
		Mask:  json.RawMessage(`"https://example.com/mask.png"`),
	}
	filter := Seedream5ImageChannelFilter(maskRequest, relayconstant.RelayModeImagesGenerations)
	if filter(genChannel) || !filter(editChannel) {
		t.Fatal("mask request was not routed to the edit-only channel")
	}

	textRequest := &dto.ImageRequest{Model: seedream5ProModel, Prompt: "edit endpoint validation"}
	filter = Seedream5ImageChannelFilter(textRequest, relayconstant.RelayModeImagesEdits)
	if filter(genChannel) || !filter(editChannel) {
		t.Fatal("/v1/images/edits was not routed to the edit-only channel")
	}
}

func TestSeedream5ImageChannelFilterKeepsNativeChannelsAvailable(t *testing.T) {
	request := &dto.ImageRequest{
		Model: seedream5LiteModel,
		Image: json.RawMessage(`"https://example.com/reference.png"`),
	}
	filter := Seedream5ImageChannelFilter(request, relayconstant.RelayModeImagesGenerations)
	native := testSeedreamChannel("http://118.196.64.1")
	if filter == nil || !filter(native) {
		t.Fatal("native Xiongda channel should remain eligible for reference images")
	}

	nonSeedream := &dto.ImageRequest{Model: "doubao-seedream-4-0-250828", Image: request.Image}
	if Seedream5ImageChannelFilter(nonSeedream, relayconstant.RelayModeImagesGenerations) != nil {
		t.Fatal("non-Seedream-5 request unexpectedly received a Seedream route filter")
	}
}

func TestSeedream5ImageChannelFilterFromContextReadsUnifiedJSONRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{
		"model":"doubao-seedream-5-0-260128",
		"prompt":"change hair",
		"image":"https://example.com/reference.png"
	}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	filter := Seedream5ImageChannelFilterFromContext(ctx, seedream5LiteModel, relayconstant.RelayModeImagesGenerations)
	if filter == nil {
		t.Fatal("expected filter from unified JSON request")
	}
	genChannel := testSeedreamChannel("https://aigateway.edgecloudapp.com/v1/account/lishuaikunpic")
	editChannel := testSeedreamChannel("https://aigateway.edgecloudapp.com/v1/account/lishuaikunpicedit")
	if filter(genChannel) || !filter(editChannel) {
		t.Fatal("context request was not classified as reference-image editing")
	}
}

func testSeedreamChannel(baseURL string) *model.Channel {
	return &model.Channel{BaseURL: &baseURL}
}
