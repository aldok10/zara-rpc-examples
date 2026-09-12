// Command gateway runs a simple REST-to-gRPC gateway — the same pattern as
// grpc-gateway but using zararpc's generated gateway adapter.
//
// Architecture:
//
//	client → :8081 (HTTP/REST) → gateway → :8080 (gRPC)
//
// One-liner to register all routes: the generated RegisterUsersServiceGateway
// creates the adapter, dials the gRPC server, and wires every HTTP endpoint.
package main

import (
	"log"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	usersv1 "github.com/aldok10/zara-rpc-examples/proto/users/v1"
	"github.com/aldok10/zara-rpc/runtime"
)

func main() {
	conn, err := grpc.NewClient("localhost:8080", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial gRPC server: %v", err)
	}
	defer conn.Close()

	// Register REST routes that proxy every call to the gRPC server.
	mux := runtime.NewMux()
	if err := usersv1.RegisterUsersServiceGateway(mux, conn); err != nil {
		log.Fatalf("register gateway: %v", err)
	}

	addr := "localhost:8081"
	log.Printf("REST gateway listening on http://%s (proxying gRPC -> localhost:8080)", addr)
	log.Printf("  curl http://%s/v1/users", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
