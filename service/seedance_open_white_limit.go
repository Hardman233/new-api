package service

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

var seedanceOpenWhiteLimiters sync.Map

type seedanceOpenWhiteLimiter struct {
	slots chan struct{}
}

func acquireSeedanceOpenWhiteSlot(ctx context.Context, scope string, limit int) (func(), error) {
	if limit <= 0 {
		return func() {}, nil
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "default"
	}
	key := fmt.Sprintf("%s:%d", scope, limit)
	value, _ := seedanceOpenWhiteLimiters.LoadOrStore(key, &seedanceOpenWhiteLimiter{
		slots: make(chan struct{}, limit),
	})
	limiter := value.(*seedanceOpenWhiteLimiter)
	select {
	case limiter.slots <- struct{}{}:
		return func() { <-limiter.slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("open-white queue wait canceled for %s: %w", scope, ctx.Err())
	}
}

func openWhiteChannelConcurrencyLimit() int {
	limit := common.GetEnvOrDefault("OPEN_WHITE_CHANNEL_CONCURRENCY_LIMIT", 1)
	if limit < 0 {
		limit = 0
	}
	return limit
}

func openWhitePeerConcurrencyLimit() int {
	limit := common.GetEnvOrDefault("OPEN_WHITE_PEER_CONCURRENCY_LIMIT", 1)
	if limit < 0 {
		return 0
	}
	return limit
}
