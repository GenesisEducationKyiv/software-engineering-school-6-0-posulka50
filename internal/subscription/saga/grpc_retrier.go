package saga

import (
	"context"
	"fmt"
	"time"

	notifierv1 "github.com/posul/github-notifier/proto/gen/notifier/v1"
)

// defaultRetryTimeout bounds a single sync retry RPC. Resend itself uses a
// 10 s client; we leave headroom for the round-trip and saga DB write that
// follows on the caller side.
const defaultRetryTimeout = 5 * time.Second

// Retrier is the gRPC-side counterpart to the broker publisher used in
// Orchestrator.Start: it calls notifier.EmailNotifierService.SendConfirmation
// synchronously so the TimeoutSweeper can rescue stuck sagas before
// compensating them.
type Retrier struct {
	client  notifierv1.EmailNotifierServiceClient
	timeout time.Duration
}

func NewRetrier(client notifierv1.EmailNotifierServiceClient) *Retrier {
	return &Retrier{client: client, timeout: defaultRetryTimeout}
}

// Retry invokes SendConfirmation with a per-call timeout. Errors are returned
// as-is so the caller can decide between retry, compensate, or log.
func (r *Retrier) Retry(ctx context.Context, sagaID, to, repo, confirmURL string) error {
	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	_, err := r.client.SendConfirmation(callCtx, &notifierv1.SendConfirmationRequest{
		SagaId:     sagaID,
		To:         to,
		Repo:       repo,
		ConfirmUrl: confirmURL,
	})
	if err != nil {
		return fmt.Errorf("grpc send confirmation: %w", err)
	}
	return nil
}
