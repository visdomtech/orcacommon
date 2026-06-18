package utils

import (
	"context"
	"log/slog"
)

// SplitLevelHandler routes log records to stdout for levels below Error,
// and to stderr for Error and above.
type SplitLevelHandler struct {
	StdHandler slog.Handler
	ErrHandler slog.Handler
}

func (h *SplitLevelHandler) Enabled(_ context.Context, level slog.Level) bool {
	if level >= slog.LevelError {
		return h.ErrHandler.Enabled(context.Background(), level)
	}
	return h.StdHandler.Enabled(context.Background(), level)
}

func (h *SplitLevelHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		return h.ErrHandler.Handle(ctx, r)
	}
	return h.StdHandler.Handle(ctx, r)
}

func (h *SplitLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SplitLevelHandler{
		StdHandler: h.StdHandler.WithAttrs(attrs),
		ErrHandler: h.ErrHandler.WithAttrs(attrs),
	}
}

func (h *SplitLevelHandler) WithGroup(name string) slog.Handler {
	return &SplitLevelHandler{
		StdHandler: h.StdHandler.WithGroup(name),
		ErrHandler: h.ErrHandler.WithGroup(name),
	}
}
