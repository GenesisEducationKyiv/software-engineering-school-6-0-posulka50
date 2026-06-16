package logctx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestHandlerInjectsRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&buf, nil)))

	ctx := WithRequestID(context.Background(), "abc-123")
	logger.InfoContext(ctx, "hello", "key", "value")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if got["request_id"] != "abc-123" {
		t.Errorf("request_id = %v, want %q", got["request_id"], "abc-123")
	}
	if got["key"] != "value" {
		t.Errorf("user attr lost: got %v", got["key"])
	}
}

func TestHandlerOmitsWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&buf, nil)))

	logger.Info("hello")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if _, ok := got["request_id"]; ok {
		t.Errorf("request_id should be absent when ctx has none, got %v", got["request_id"])
	}
}

func TestRequestIDNilContext(t *testing.T) {
	var ctx context.Context
	if got := RequestID(ctx); got != "" {
		t.Errorf("RequestID(nil) = %q, want empty", got)
	}
}
