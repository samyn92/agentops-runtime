/*
Agent Runtime — Fantasy (Go)

Tool call repair: when the model generates a malformed tool call (invalid JSON,
missing required params), the SDK invokes this function to attempt repair.
We log the error and return nil to let the SDK fall back to sending the
validation error back to the model as a tool result (self-repair loop).
*/
package main

import (
	"context"
	"log/slog"

	"charm.land/fantasy"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// defaultRepairToolCall is the default repair function for malformed tool calls.
// It records the validation error as a trace event and returns nil to trigger
// the SDK's built-in self-repair loop (sends the error back to the model).
func defaultRepairToolCall(ctx context.Context, opts fantasy.ToolCallRepairOptions) (*fantasy.ToolCallContent, error) {
	slog.Warn("tool call validation failed, requesting model self-repair",
		"tool", opts.OriginalToolCall.ToolName,
		"error", opts.ValidationError.Error(),
	)

	// Record on the active span for observability in Tempo
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.AddEvent("tool_call.repair_requested", trace.WithAttributes(
			attribute.String("tool.name", opts.OriginalToolCall.ToolName),
			attribute.String("tool.validation_error", opts.ValidationError.Error()),
			attribute.String("tool.original_input", truncate(opts.OriginalToolCall.Input, 500)),
		))
	}

	// Return nil to let the SDK handle repair by sending the error back to the model
	return nil, nil
}
