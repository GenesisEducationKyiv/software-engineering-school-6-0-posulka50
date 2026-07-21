package grpcsrv

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc"
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

func TestSendConfirmation_UpstreamUnavailable_MapsToUnavailable(t *testing.T) {
	sender := &fakeSender{err: fmt.Errorf("send resend request: %w", domain.ErrUpstreamUnavailable)}
	dedupe := NewDedupe(10, time.Hour)
	srv := NewServer(sender, dedupe)

	_, err := srv.SendConfirmation(context.Background(), validRequest())
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Errorf("expected codes.Unavailable, got %s", st.Code())
	}
	if dedupe.Seen("saga-1") {
		t.Error("expected saga to be released after transport failure")
	}
}

func TestSendConfirmation_DeadlineExceeded_MapsToDeadlineExceeded(t *testing.T) {
	sender := &fakeSender{err: fmt.Errorf("send resend request: %w", context.DeadlineExceeded)}
	dedupe := NewDedupe(10, time.Hour)
	srv := NewServer(sender, dedupe)

	_, err := srv.SendConfirmation(context.Background(), validRequest())
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.DeadlineExceeded {
		t.Errorf("expected codes.DeadlineExceeded, got %s", st.Code())
	}
}

func TestSendConfirmation_UnknownError_MapsToInternal(t *testing.T) {
	sender := &fakeSender{err: errors.New("render failed")}
	dedupe := NewDedupe(10, time.Hour)
	srv := NewServer(sender, dedupe)

	_, err := srv.SendConfirmation(context.Background(), validRequest())
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("expected codes.Internal, got %s", st.Code())
	}
}

// TestValidationInterceptor_RejectsInvalidRequests covers the field-shape
// contract now expressed in notification.proto (protovalidate rules). Runs
// the interceptor directly with a stub handler so the test does not rely on
// spinning up a real gRPC server.
func TestValidationInterceptor_RejectsInvalidRequests(t *testing.T) {
	interceptor := NewValidationUnaryInterceptor()
	senderCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		senderCalled = true
		return &notifierv1.SendConfirmationResponse{}, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/notifier.v1.EmailNotifierService/SendConfirmation"}

	cases := map[string]*notifierv1.SendConfirmationRequest{
		"missing_saga_id":     {To: "user@example.com", Repo: "golang/go", ConfirmUrl: "https://example.com/c/tok"},
		"missing_to":          {SagaId: "s", Repo: "golang/go", ConfirmUrl: "https://example.com/c/tok"},
		"invalid_to_email":    {SagaId: "s", To: "not-an-email", Repo: "golang/go", ConfirmUrl: "https://example.com/c/tok"},
		"missing_repo":        {SagaId: "s", To: "user@example.com", ConfirmUrl: "https://example.com/c/tok"},
		"invalid_repo_shape":  {SagaId: "s", To: "user@example.com", Repo: "no-slash", ConfirmUrl: "https://example.com/c/tok"},
		"missing_confirm_url": {SagaId: "s", To: "user@example.com", Repo: "golang/go"},
		"invalid_confirm_url": {SagaId: "s", To: "user@example.com", Repo: "golang/go", ConfirmUrl: "not a url"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			senderCalled = false
			_, err := interceptor(context.Background(), req, info, handler)
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected gRPC status error, got %v", err)
			}
			if st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got %s", st.Code())
			}
			if senderCalled {
				t.Error("expected handler NOT to be called on validation failure")
			}
		})
	}
}

func TestValidationInterceptor_AllowsValidRequest(t *testing.T) {
	interceptor := NewValidationUnaryInterceptor()
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return &notifierv1.SendConfirmationResponse{}, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/notifier.v1.EmailNotifierService/SendConfirmation"}

	_, err := interceptor(context.Background(), validRequest(), info, handler)
	if err != nil {
		t.Fatalf("expected valid request to pass, got %v", err)
	}
}
