package service

import (
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/model"
)

var channelConcurrencyLocks sync.Map

type channelConcurrencyFilterState struct {
	saturated []string
}

func newChannelConcurrencyFilterState() *channelConcurrencyFilterState {
	return &channelConcurrencyFilterState{}
}

func (s *channelConcurrencyFilterState) Filter(channel *model.Channel) bool {
	err := CheckChannelConcurrencyLimit(channel)
	if err == nil {
		return true
	}
	if s != nil {
		s.saturated = append(s.saturated, err.Error())
	}
	return false
}

func (s *channelConcurrencyFilterState) SaturatedError(group, modelName string) error {
	if s == nil || len(s.saturated) == 0 {
		return nil
	}
	detail := strings.Join(s.saturated, "; ")
	return fmt.Errorf("no available channel because concurrency limit is reached, group: %s, model: %s, detail: %s", group, modelName, detail)
}

func CheckChannelConcurrencyLimit(channel *model.Channel) error {
	if channel == nil {
		return nil
	}
	limit := channel.GetConcurrencyLimit()
	if limit <= 0 {
		return nil
	}
	active, err := model.CountUnfinishedTasksByChannel(channel.Id)
	if err != nil {
		return fmt.Errorf("check channel #%d concurrency failed: %w", channel.Id, err)
	}
	if active >= int64(limit) {
		return fmt.Errorf("channel #%d %s concurrency limit reached: %d/%d unfinished tasks", channel.Id, channel.Name, active, limit)
	}
	return nil
}

func LockChannelConcurrencyIfLimited(channel *model.Channel) func() {
	if channel == nil || channel.GetConcurrencyLimit() <= 0 {
		return func() {}
	}
	value, _ := channelConcurrencyLocks.LoadOrStore(channel.Id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
