package xaigrpc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/xaiapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

func Start(handler http.Handler) (*grpc.Server, error) {
	if !common.GetEnvOrDefaultBool("XAI_GRPC_ENABLED", false) {
		return nil, nil
	}
	port := strings.TrimSpace(os.Getenv("XAI_GRPC_PORT"))
	if port == "" {
		port = "3001"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf("invalid XAI_GRPC_PORT %q", port)
	}
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return nil, fmt.Errorf("listen for xAI gRPC: %w", err)
	}
	maxMessageMB := common.GetEnvOrDefault("XAI_GRPC_MAX_MESSAGE_MB", 512)
	if maxMessageMB <= 0 {
		maxMessageMB = 512
	}
	maxMessageBytes := maxMessageMB * 1024 * 1024
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxMessageBytes),
		grpc.MaxSendMsgSize(maxMessageBytes),
		grpc.ChainUnaryInterceptor(recoverUnary),
		grpc.ChainStreamInterceptor(recoverStream),
	)
	bridge := &httpBridge{handler: handler}
	xaiapi.RegisterVideoServer(grpcServer, &videoServer{bridge: bridge})
	xaiapi.RegisterFilesServer(grpcServer, &filesServer{bridge: bridge})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	if common.GetEnvOrDefaultBool("XAI_GRPC_REFLECTION_ENABLED", false) {
		reflection.Register(grpcServer)
	}
	go func() {
		common.SysLog("xAI official gRPC API listening on port " + port)
		if serveErr := grpcServer.Serve(listener); serveErr != nil {
			common.SysError("xAI gRPC server stopped: " + serveErr.Error())
		}
	}()
	return grpcServer, nil
}

func recoverUnary(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("panic in xAI gRPC %s: %v\n%s", info.FullMethod, recovered, debug.Stack()))
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(ctx, request)
}

func recoverStream(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("panic in xAI gRPC %s: %v\n%s", info.FullMethod, recovered, debug.Stack()))
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(server, stream)
}
