package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestWangsuAnthropicChannelUsesDedicatedAPIType(t *testing.T) {
	apiType, ok := ChannelType2APIType(constant.ChannelTypeWangsuAnthropic)
	require.True(t, ok)
	require.Equal(t, constant.APITypeWangsuAnthropic, apiType)
	require.Equal(t, "WangsuAnthropic", constant.GetChannelTypeName(constant.ChannelTypeWangsuAnthropic))
	require.Equal(t,
		"https://api.edgecloudapp.com/v2/llm",
		constant.ChannelBaseURLs[constant.ChannelTypeWangsuAnthropic],
	)
}

func TestWangsuAnthropicChannelEndpointTypes(t *testing.T) {
	endpointTypes := GetEndpointTypesByChannelType(constant.ChannelTypeWangsuAnthropic, "claude-sonnet-4-6")
	require.Equal(t, []constant.EndpointType{
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeOpenAI,
	}, endpointTypes)
	require.NotContains(t, endpointTypes, constant.EndpointTypeOpenAIResponse)
}
