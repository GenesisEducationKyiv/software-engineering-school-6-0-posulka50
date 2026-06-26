package grpcsrv

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/posul/github-notifier/internal/notifier/domain"
	notifierv1 "github.com/posul/github-notifier/proto/gen/notifier/v1"
)

type fakeSender struct {
	calls []struct {
		to   string
		data domain.ConfirmData
	}
	err error
}

func (f *fakeSender) SendConfirmation(_ context.Context, to string, data domain.ConfirmData) error {
	f.calls = append(f.calls, struct {
		to   string
		data domain.ConfirmData
	}{to, data})
	return f.err
}

func validRequest() *notifierv1.SendConfirmationRequest {
	return &notifierv1.SendConfirmationRequest{
		SagaId:     "saga-1",
		To:         "user@example.com",
		Repo:       "golang/go",
		ConfirmUrl: "https://example.com/confirm/tok",
	}
}

func TestSendConfirmation_HappyPath(t *testing.T) {
	sender := &fakeSender{}
	dedupe := NewDedupe(10, time.Hour)
	srv := NewServer(sender, dedupe)

	resp, err := srv.SendConfirmation(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if len(sender.calls) != 1 {
		t.Fatalf("expected 1 send call, got %d", len(sender.calls))
	}
	c := sender.calls[0]
	if c.to != "user@example.com" || c.data.Repo != "golang/go" || c.data.ConfirmURL != "https://example.com/confirm/tok" {
		t.Errorf("unexpected sender args: %+v", c)
	}
	if !dedupe.Seen("saga-1") {
		t.Error("expected saga to be marked in dedupe after successful send")
	}
}

func TestSendConfirmation_DedupeHit_SkipsSenderAndReturnsOK(t *testing.T) {
	sender := &fakeSender{}
	dedupe := NewDedupe(10, time.Hour)
	dedupe.Mark("saga-1")
	srv := NewServer(sender, dedupe)

	resp, err := srv.SendConfirmation(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("expected OK on dedupe hit, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if len(sender.calls) != 0 {
		t.Errorf("expected sender NOT to be called on dedupe hit, got %d calls", len(sender.calls))
	}
}

func TestSendConfirmation_SenderError_MapsToInternalAndSkipsMark(t *testing.T) {
	sender := &fakeSender{err: errors.New("resend returned status 500")}
	dedupe := NewDedupe(10, time.Hour)
	srv := NewServer(sender, dedupe)

	_, err := srv.SendConfirmation(context.Background(), validRequest())
	if err == nil {
		t.Fatal("expected error when sender fails")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("expected codes.Internal, got %s", st.Code())
	}
	if dedupe.Seen("saga-1") {
		t.Error("expected saga NOT to be marked when sender fails")
	}
}

func TestSendConfirmation_MissingFields_InvalidArgument(t *testing.T) {
	cases := map[string]*notifierv1.SendConfirmationRequest{
		"missing_saga_id":     {To: "u@e.com", Repo: "x/y", ConfirmUrl: "u"},
		"missing_to":          {SagaId: "s", Repo: "x/y", ConfirmUrl: "u"},
		"missing_repo":        {SagaId: "s", To: "u@e.com", ConfirmUrl: "u"},
		"missing_confirm_url": {SagaId: "s", To: "u@e.com", Repo: "x/y"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			srv := NewServer(&fakeSender{}, NewDedupe(10, time.Hour))
			_, err := srv.SendConfirmation(context.Background(), req)
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected gRPC status error, got %v", err)
			}
			if st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got %s", st.Code())
			}
		})
	}
}
