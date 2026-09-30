package xai

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/xaiapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	defaultAPIHost        = "api.x.ai:443"
	defaultRequestTimeout = 30 * time.Second
)

var grpcConnections sync.Map

type videoClientFactory func(baseURL string) (xaiapi.VideoClient, error)

var newVideoClient videoClientFactory = pooledVideoClient

func pooledVideoClient(baseURL string) (xaiapi.VideoClient, error) {
	target, useTLS, serverName, err := normalizeGRPCTarget(baseURL)
	if err != nil {
		return nil, err
	}
	cacheKey := fmt.Sprintf("%t|%s", useTLS, target)
	if cached, ok := grpcConnections.Load(cacheKey); ok {
		return xaiapi.NewVideoClient(cached.(*grpc.ClientConn)), nil
	}

	opts := []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(upstreamMaxMessageBytes()),
			grpc.MaxCallRecvMsgSize(upstreamMaxMessageBytes()),
		),
		grpc.WithDefaultServiceConfig(`{
			"methodConfig": [{
				"name": [{}],
				"retryPolicy": {
					"maxAttempts": 5,
					"initialBackoff": "0.1s",
					"maxBackoff": "1s",
					"backoffMultiplier": 2,
					"retryableStatusCodes": ["UNAVAILABLE"]
				}
			}]
		}`),
	}
	if useTLS {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		})))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("create xAI gRPC client: %w", err)
	}
	actual, loaded := grpcConnections.LoadOrStore(cacheKey, conn)
	if loaded {
		_ = conn.Close()
		conn = actual.(*grpc.ClientConn)
	}
	return xaiapi.NewVideoClient(conn), nil
}

func upstreamMaxMessageBytes() int {
	megabytes := common.GetEnvOrDefault("XAI_UPSTREAM_GRPC_MAX_MESSAGE_MB", 512)
	if megabytes <= 0 {
		megabytes = 512
	}
	return megabytes * 1024 * 1024
}

func normalizeGRPCTarget(raw string) (target string, useTLS bool, serverName string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultAPIHost, true, "api.x.ai", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, "", fmt.Errorf("parse xAI base URL: %w", err)
	}
	host := strings.TrimSpace(u.Host)
	if host == "" {
		return "", false, "", fmt.Errorf("xAI base URL has no host")
	}
	hostname := u.Hostname()
	useTLS = !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "grpc")
	if _, _, splitErr := net.SplitHostPort(host); splitErr != nil {
		if !useTLS {
			host = net.JoinHostPort(hostname, "80")
		} else {
			host = net.JoinHostPort(hostname, "443")
		}
	}
	return host, useTLS, hostname, nil
}

func outgoingContext(parent context.Context, apiKey string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, defaultRequestTimeout)
	ctx = metadata.AppendToOutgoingContext(
		ctx,
		"authorization", "Bearer "+strings.TrimSpace(apiKey),
		"xai-sdk-version", "new-api/xai-video-compat-1",
		"xai-sdk-language", "go",
	)
	return ctx, cancel
}
