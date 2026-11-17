package tdu

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
)

// traceNativeServices decorates local handlers at the composition boundary so
// transport code need not know the edNet envelope or depend on its services.
func traceNativeServices(log *slog.Logger, handlers map[uint16]nativeproxy.DataHandler, batches map[uint16]nativeproxy.BatchDataHandler) {
	for port, handle := range handlers {
		handlers[port] = func(ctx context.Context, info nativeproxy.SessionInfo, destination netip.AddrPort, packet []byte) ([]byte, error) {
			reply, err := handle(ctx, info, destination, packet)
			count := 0
			if len(reply) != 0 {
				count = 1
			}
			traceNativeRequest(ctx, log, info, destination, packet, count, err)
			return reply, err
		}
	}
	for port, handle := range batches {
		batches[port] = func(ctx context.Context, info nativeproxy.SessionInfo, destination netip.AddrPort, packet []byte) ([][]byte, error) {
			replies, err := handle(ctx, info, destination, packet)
			traceNativeRequest(ctx, log, info, destination, packet, len(replies), err)
			return replies, err
		}
	}
}

func traceNativeRequest(ctx context.Context, log *slog.Logger, info nativeproxy.SessionInfo, destination netip.AddrPort, packet []byte, replies int, err error) {
	if !log.Enabled(ctx, slog.LevelDebug) {
		return
	}
	requestLog := log.With(
		"remote", info.Remote,
		"session", info.ID,
		"generation", info.Generation,
		"logical_port", destination.Port(),
		"request_bytes", len(packet),
		"reply_count", replies,
	)
	frame, parseErr := ednet.ParseFrame(packet)
	if parseErr == nil {
		requestLog = requestLog.With("envelope", fmt.Sprintf("0x%04x", frame.Kind), "request_id", frame.RequestID)
		if len(frame.Action) >= 2 {
			requestLog = requestLog.With("action", fmt.Sprintf("0x%02x%02x", frame.Action[0], frame.Action[1]))
		}
	} else {
		requestLog = requestLog.With("frame_valid", false)
	}
	if err != nil {
		requestLog.DebugContext(ctx, "native service request rejected", "reason", err.Error())
		return
	}
	requestLog.DebugContext(ctx, "native service request completed")
}
