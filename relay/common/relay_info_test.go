package common

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestRelayInfoGetFinalRequestRelayFormatPrefersExplicitFinal(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		RequestConversionChain:  []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
		FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToConversionChain(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToRelayFormat(t *testing.T) {
	info := &RelayInfo{
		RelayFormat: types.RelayFormatGemini,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatGemini), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatNilReceiver(t *testing.T) {
	var info *RelayInfo
	require.Equal(t, types.RelayFormat(""), info.GetFinalRequestRelayFormat())
}

func TestTaskSubmitReqAcceptsVideoImageObjectAndOutput(t *testing.T) {
	var request TaskSubmitReq
	err := json.Unmarshal([]byte(`{
		"prompt":"animate the first frame",
		"image":{"url":"https://example.com/frame.png"},
		"output":{"upload_url":"https://uploads.example.com/video"}
	}`), &request)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/frame.png", request.ImageURL)
	require.NotNil(t, request.Output)
	require.Equal(t, "https://uploads.example.com/video", request.Output.UploadURL)
}

func TestTaskSubmitReqKeepsStringImageCompatibility(t *testing.T) {
	var request TaskSubmitReq
	err := json.Unmarshal([]byte(`{"prompt":"animate","image":"https://example.com/frame.png"}`), &request)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/frame.png", request.Image)
}
