// Package grpcsrv exposes the notifier as a gRPC service. It is the synchronous
// counterpart to the RabbitMQ consumer: both targets call the same Sender and
// share a Dedupe so an inbound retry that races the original async send does
// not deliver a duplicate confirmation email.
package grpcsrv

import (
	"context"
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
// again.
func (s *Server) SendConfirmation(ctx context.Context, req *notifierv1.SendConfirmationRequest) (*notifierv1.SendConfirmationResponse, error) {
	if req.GetSagaId() == "" {
		return nil, status.Error(codes.InvalidArgument, "saga_id is required")
	}
	if req.GetTo() == "" {
		return nil, status.Error(codes.InvalidArgument, "to is required")
	}
	if req.GetRepo() == "" {
		return nil, status.Error(codes.InvalidArgument, "repo is required")
	}
	if req.GetConfirmUrl() == "" {
		return nil, status.Error(codes.InvalidArgument, "confirm_url is required")
	}

	if s.dedupe.Seen(req.GetSagaId()) {
		slog.InfoContext(ctx, "grpc: dedupe hit, skipping confirmation send",
			"saga_id", req.GetSagaId(), "to", req.GetTo())
		return &notifierv1.SendConfirmationResponse{}, nil
	}

	if err := s.sender.SendConfirmation(ctx, req.GetTo(), domain.ConfirmData{
		Repo:       req.GetRepo(),
		ConfirmURL: req.GetConfirmUrl(),
	}); err != nil {
		slog.ErrorContext(ctx, "grpc: send confirmation failed",
			"saga_id", req.GetSagaId(), "to", req.GetTo(), "repo", req.GetRepo(), "error", err)
		return nil, status.Errorf(codes.Internal, "send confirmation: %v", err)
	}

	s.dedupe.Mark(req.GetSagaId())
	slog.InfoContext(ctx, "grpc: confirmation sent",
		"saga_id", req.GetSagaId(), "to", req.GetTo(), "repo", req.GetRepo())
	return &notifierv1.SendConfirmationResponse{}, nil
}
