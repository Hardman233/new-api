package youchuan

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/QuantumNous/new-api/model"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

var failedComments = map[string]struct{}{
	"jobstatusfail":                   {},
	"jobstatuserror":                  {},
	"jobstatusreject":                 {},
	"jobstatustextreject":             {},
	"jobstatusbadprompt":              {},
	"jobstatusinvalidparameter":       {},
	"jobstatustimeout":                {},
	"jobstatusrequesttimeout":         {},
	"jobstatusinvalidimagepromptlink": {},
	"jobstatusmaxconcurrentlimited":   {},
	"jobstatuscreditnotenough":        {},
	"jobstatuscanceled":               {},
	"jobstatusimagepromptdenied":      {},
	"jobstatusduplicateimage":         {},
}

func ParseTaskResponse(response TaskResponse) *relaycommon.TaskInfo {
	result := &relaycommon.TaskInfo{
		TaskID:         response.ID,
		ProviderStatus: fmt.Sprintf("%d:%s", response.Status, response.Comment),
	}
	comment := strings.ToLower(strings.TrimSpace(response.Comment))
	switch {
	case response.Status == 3 || isFailedComment(comment):
		result.Status = model.TaskStatusFailure
		result.Progress = taskcommon.ProgressComplete
		result.Reason = firstNonEmpty(response.Comment, "Youchuan task failed")
	case response.Status == 2 || comment == "jobstatussuccess":
		result.Status = model.TaskStatusSuccess
		result.Progress = taskcommon.ProgressComplete
	case comment == "jobstatusqueued":
		result.Status = model.TaskStatusQueued
		result.Progress = taskcommon.ProgressQueued
	case response.Status == 0 || comment == "jobstatuscreated":
		result.Status = model.TaskStatusSubmitted
		result.Progress = taskcommon.ProgressSubmitted
	default:
		result.Status = model.TaskStatusInProgress
		result.Progress = taskcommon.ProgressInProgress
	}

	for index, remoteURL := range response.URLs {
		audit := ""
		if index < len(response.Audits) {
			audit = response.Audits[index]
		}
		output := relaycommon.TaskOutput{
			Index:     index,
			RemoteURL: strings.TrimSpace(remoteURL),
			Audit:     audit,
			MediaKind: mediaKindForURL(remoteURL),
		}
		result.Outputs = append(result.Outputs, output)
		if result.Url == "" && output.RemoteURL != "" {
			result.Url = output.RemoteURL
			result.RemoteUrl = output.RemoteURL
		}
	}
	return result
}

func isFailedComment(comment string) bool {
	_, ok := failedComments[comment]
	return ok
}

func mediaKindForURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err == nil {
		ext := strings.ToLower(path.Ext(parsed.Path))
		if ext == ".mp4" || ext == ".mov" || ext == ".webm" || ext == ".m3u8" {
			return model.GeneratedMediaKindVideo
		}
	}
	return model.GeneratedMediaKindImage
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
