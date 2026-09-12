module github.com/aldok10/zara-rpc-examples

go 1.27.0

require (
	github.com/aldok10/zara-rpc v0.0.0
	golang.org/x/net v0.59.0
	google.golang.org/genproto/googleapis/api v0.0.0-20260908043556-f8649ddbbfe6
	google.golang.org/grpc v1.83.1
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/coder/websocket v1.8.15 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260904194346-d0f1323225a4 // indirect
)

replace github.com/aldok10/zara-rpc => ../
