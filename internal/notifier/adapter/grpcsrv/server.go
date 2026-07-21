// Package grpcsrv exposes the notifier as a gRPC service. It is the synchronous
// counterpart to the RabbitMQ consumer: both targets call the same Sender and
// share a Dedupe so an inbound retry that races the original async send does
// not deliver a duplicate confirmation email.
package grpcsrv

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/posul/github-notifier/internal/notifier/domain"
	notifierv1 "github.com/posul/github-notifier/proto/gen/notifier/v1"
)

// Sender is the email-sending port. Satisfied by *resend.Sender via
// structural typing; the same interface is used by the RabbitMQ consumer.
type Sender interface {
	SendConfirmation(ctx context.Context, to string, data domain.ConfirmData) error
}

// Server implements notifierv1.EmailNotifierServiceServer.
type Server struct {
	notifierv1.UnimplementedEmailNotifierServiceServer
	sender Sender
	dedupe *Dedupe
}

func NewServer(sender Sender, dedupe *Dedupe) *Server {
	return &Server{sender: sender, dedupe: dedupe}
}

// SendConfirmation renders and sends the saga's confirmation email. The
// dedupe check ensures that when the RabbitMQ path already delivered the
// email for this saga, a sweeper-triggered retry returns OK without sending
// again. Field-shape validation is handled by the protovalidate interceptor
// wired at server construction; by the time this method runs, the request
// is already known-good against the .proto constraints.
func (s *Server) SendConfirmation(ctx context.Context, req *notifierv1.SendConfirmationRequest) (*notifierv1.SendConfirmationResponse, error) {
	// Atomic check-and-mark: only one concurrent caller for a given saga_id
	// wins the claim and proceeds to Resend; every other caller returns OK
	// without sending. If the send fails we Release so a legitimate retry is
	// not permanently blocked.
	if !s.dedupe.TryClaim(req.GetSagaId()) {
		slog.InfoContext(ctx, "grpc: dedupe hit, skipping confirmation send",
			"saga_id", req.GetSagaId(), "to", req.GetTo())
		return &notifierv1.SendConfirmationResponse{}, nil
	}

	if err := s.sender.SendConfirmation(ctx, req.GetTo(), domain.ConfirmData{
		Repo:       req.GetRepo(),
		ConfirmURL: req.GetConfirmUrl(),
	}); err != nil {
		s.dedupe.Release(req.GetSagaId())
		slog.ErrorContext(ctx, "grpc: send confirmation failed",
			"saga_id", req.GetSagaId(), "to", req.GetTo(), "repo", req.GetRepo(), "error", err)
		return nil, sendErrorToStatus(err)
	}

	slog.InfoContext(ctx, "grpc: confirmation sent",
		"saga_id", req.GetSagaId(), "to", req.GetTo(), "repo", req.GetRepo())
	return &notifierv1.SendConfirmationResponse{}, nil
}

// sendErrorToStatus maps a Sender error to the gRPC status codes promised by
// the proto contract: DeadlineExceeded when the caller/upstream ran out of
// time, Unavailable for transient transport failures to the email provider,
// Internal for anything else (render errors, marshal errors, 4xx from Resend).
func sendErrorToStatus(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return status.Errorf(codes.DeadlineExceeded, "send confirmation: %v", err)
	case errors.Is(err, domain.ErrUpstreamUnavailable):
		return status.Errorf(codes.Unavailable, "send confirmation: %v", err)
	default:
		return status.Errorf(codes.Internal, "send confirmation: %v", err)
	}
}
