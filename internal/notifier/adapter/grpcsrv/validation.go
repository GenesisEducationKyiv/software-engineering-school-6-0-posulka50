package grpcsrv

import (
	"context"

	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// NewValidationUnaryInterceptor returns a gRPC unary interceptor that runs
// protovalidate rules embedded in the request .proto against every inbound
// message. Validation failures short-circuit with codes.InvalidArgument so
// the handler never sees a malformed request. Keeps the field-shape contract
// in the .proto (single source of truth) rather than in hand-written checks.
func NewValidationUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if msg, ok := req.(proto.Message); ok {
			if err := protovalidate.Validate(msg); err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
		}
		return handler(ctx, req)
	}
}
