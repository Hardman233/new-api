package wangsu_anthropic

import (
	"io"
	"strings"

	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// Adaptor reuses the Anthropic protocol implementation with a provider-specific
// channel type, default endpoint, and documented model list.
type Adaptor struct {
	claude.Adaptor
}

var _ channel.Adaptor = (*Adaptor)(nil)

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseURL := strings.TrimRight(info.ChannelBaseUrl, "/")
	if !strings.HasSuffix(baseURL, "/anthropic") {
		baseURL += "/anthropic"
	}

	clonedInfo := *info
	clonedInfo.ChannelBaseUrl = baseURL
	return a.Adaptor.GetRequestURL(&clonedInfo)
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) GetModelList() []string {
	return append([]string(nil), ModelList...)
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
