package saga_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/posul/github-notifier/internal/subscription/saga"
	notifierv1 "github.com/posul/github-notifier/proto/gen/notifier/v1"
)

type stubGRPCServer struct {
	notifierv1.UnimplementedEmailNotifierServiceServer
	receivedReq *notifierv1.SendConfirmationRequest
	respondWith error
	delay       time.Duration
}

func (s *stubGRPCServer) SendConfirmation(ctx context.Context, req *notifierv1.SendConfirmationRequest) (*notifierv1.SendConfirmationResponse, error) {
	s.receivedReq = req
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.respondWith != nil {
		return nil, s.respondWith
	}
	return &notifierv1.SendConfirmationResponse{}, nil
}

func startBufconnServer(t *testing.T, stub *stubGRPCServer) notifierv1.EmailNotifierServiceClient {
	t.Helper()
	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	srv := grpc.NewServer()
	notifierv1.RegisterEmailNotifierServiceServer(srv, stub)
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.DialContext(context.Background())
	}
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return notifierv1.NewEmailNotifierServiceClient(conn)
}

func TestRetrier_Success_PassesArgsThrough(t *testing.T) {
	stub := &stubGRPCServer{}
	client := startBufconnServer(t, stub)
	r := saga.NewRetrier(client)

	err := r.Retry(context.Background(), "saga-1", "u@example.com", "golang/go", "https://x/confirm/tok")
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if stub.receivedReq == nil {
		t.Fatal("expected server to receive request")
	}
	if stub.receivedReq.GetSagaId() != "saga-1" ||
		stub.receivedReq.GetTo() != "u@example.com" ||
		stub.receivedReq.GetRepo() != "golang/go" ||
		stub.receivedReq.GetConfirmUrl() != "https://x/confirm/tok" {
		t.Errorf("request fields mismatch: %+v", stub.receivedReq)
	}
}

func TestRetrier_ServerError_Propagates(t *testing.T) {
	stub := &stubGRPCServer{respondWith: status.Error(codes.Internal, "boom")}
	client := startBufconnServer(t, stub)
	r := saga.NewRetrier(client)

	err := r.Retry(context.Background(), "saga-1", "u@e.com", "r/p", "u")
	if err == nil {
		t.Fatal("expected error")
	}
	st, ok := status.FromError(errors.Unwrap(err))
	if !ok {
		t.Fatalf("expected unwrapped status error, got %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("expected Internal, got %s", st.Code())
	}
}

func TestRetrier_Unavailable_Propagates(t *testing.T) {
	stub := &stubGRPCServer{respondWith: status.Error(codes.Unavailable, "down")}
	client := startBufconnServer(t, stub)
	r := saga.NewRetrier(client)

	err := r.Retry(context.Background(), "saga-1", "u@e.com", "r/p", "u")
	if err == nil {
		t.Fatal("expected error")
	}
	st, ok := status.FromError(errors.Unwrap(err))
	if !ok {
		t.Fatalf("expected unwrapped status error, got %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Errorf("expected Unavailable, got %s", st.Code())
	}
}
