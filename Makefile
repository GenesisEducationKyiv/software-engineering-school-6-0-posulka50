.PHONY: proto-lint proto-gen proto-tools

proto-lint:
	buf lint

proto-gen:
	buf generate

proto-tools:
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
