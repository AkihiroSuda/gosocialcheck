package progress

import (
	"context"
	"log/slog"
)

type Event struct {
	Message string `json:"message,omitempty"`
}

type Handler func(context.Context, Event)

func DefaultHandler(ctx context.Context, ev Event) {
	slog.DebugContext(ctx, "progress: "+ev.Message)
}
