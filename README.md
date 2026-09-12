# zara-rpc examples

Example `UsersService` for [zara-rpc](https://github.com/aldok10/zara-rpc): a combined gRPC + HTTP server, a REST-to-gRPC gateway, and an end-to-end client demo exercising JSON, Protobuf, SSE, NDJSON, and WebSocket.

This repository is designed to be used as a **git submodule** of `github.com/aldok10/zara-rpc` at `examples/`:

```sh
git clone --recurse-submodules https://github.com/aldok10/zara-rpc
cd zara-rpc/examples
go run ./server
```

The `go.mod` uses `replace github.com/aldok10/zara-rpc => ../`, which resolves to the parent repository checkout when used as a submodule. For standalone development, clone `zara-rpc` next to this repo and change the replace path to `../zara-rpc`.

## Run

```sh
go run ./server    # combined gRPC + HTTP on :8080
go run ./gateway   # REST -> gRPC gateway on :8081
go run ./client    # end-to-end demo
```

## Regenerate

Requires `protoc-gen-zararpc` on `PATH` (install: `go install github.com/aldok10/zara-rpc/protoc-gen-zararpc@latest`):

```sh
buf generate
```

Generated files are committed; never hand-edit `*.zararpc.go`, `*.gateway.go`, `*.pb.go`, `*_grpc.pb.go`.