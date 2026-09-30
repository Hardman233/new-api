package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	taskyouchuan "github.com/QuantumNous/new-api/relay/channel/task/youchuan"
	"github.com/stretchr/testify/require"
)

func TestSelectYouchuanCallbackTaskMatchesCredentialFingerprint(t *testing.T) {
	credentialA := taskyouchuan.Credential{AppID: "app-a", Secret: "secret-a"}
	credentialB := taskyouchuan.Credential{AppID: "app-b", Secret: "secret-b"}
	taskA := &model.Task{ID: 1, PrivateData: model.TaskPrivateData{Youchuan: &model.YouchuanTaskPrivateData{
		CredentialFingerprint: taskyouchuan.CredentialFingerprint(credentialA),
	}}}
	taskB := &model.Task{ID: 2, PrivateData: model.TaskPrivateData{Youchuan: &model.YouchuanTaskPrivateData{
		CredentialFingerprint: taskyouchuan.CredentialFingerprint(credentialB),
	}}}

	selected := selectYouchuanCallbackTask([]*model.Task{taskB, taskA}, taskyouchuan.CredentialFingerprint(credentialA))
	require.Same(t, taskA, selected)
	require.Nil(t, selectYouchuanCallbackTask([]*model.Task{taskA, taskB}, "not-a-match"))
}

func TestApplyYouchuanResponsePrivateDataDetectsVideoOutputs(t *testing.T) {
	private := &model.YouchuanTaskPrivateData{}
	applyYouchuanResponsePrivateData(private, taskyouchuan.TaskResponse{
		ID:     "job-video",
		URLs:   []string{"https://example.com/result.mp4", ""},
		Audits: []string{"", "rejected"},
	})

	require.Len(t, private.Outputs, 2)
	require.Equal(t, model.GeneratedMediaKindVideo, private.Outputs[0].MediaKind)
	require.Equal(t, model.GeneratedMediaKindImage, private.Outputs[1].MediaKind)
	require.Equal(t, "rejected", private.Outputs[1].Audit)
}
