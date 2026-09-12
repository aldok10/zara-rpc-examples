// Command client demonstrates the generated SDK (UsersServiceHTTPClient)
// talking to the combined server on :8080 over plain HTTP. The same client
// works against the gateway on :8081.
//
// The codec is chosen per client via client.WithCodec:
//
//	JSON      (default)  — application/json
//	Protobuf             — application/protobuf (binary)
//
// Per-call options (extraOpts) partially merge with the base config, e.g.
// client.WithHeader("x-trace", "abc") adds a header for one call only.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	usersv1 "github.com/aldok10/zara-rpc-examples/proto/users/v1"
	"github.com/aldok10/zara-rpc/client"
	"github.com/aldok10/zara-rpc/encoding"
	"github.com/aldok10/zara-rpc/metadata"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	baseURL := "http://localhost:8080"

	// ---- JSON client (default codec) ----
	jsonClient := usersv1.NewUsersServiceHTTPClient(baseURL)
	fmt.Println("== JSON client ==")
	runUnary(ctx, jsonClient)

	// ---- Protobuf client (binary codec) ----
	protoClient := usersv1.NewUsersServiceHTTPClient(baseURL, client.WithCodec(encoding.ProtoCodec{}))
	fmt.Println("== Protobuf client ==")
	runUnary(ctx, protoClient)

	// ---- Streaming over JSON ----
	fmt.Println("== Streaming (JSON) ==")
	runStreaming(ctx, jsonClient)

	// ---- Server streaming over WebSocket ----
	fmt.Println("== Server streaming (WebSocket) ==")
	wsClient := usersv1.NewUsersServiceHTTPClient(baseURL, client.WithServerStreamTransport(client.ServerStreamWebSocket))
	watchWS, err := wsClient.WatchUsers(ctx, &usersv1.WatchUsersRequest{IntervalSeconds: 1})
	if err != nil {
		log.Fatalf("WatchUsers(ws): %v", err)
	}
	defer watchWS.Close()
	fmt.Println("WatchUsers  -> (WebSocket, first 2 events)")
	for i := 0; i < 2; i++ {
		u, err := watchWS.Receive()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("WatchUsers(ws) recv: %v", err)
		}
		fmt.Printf("  event: id=%s name=%s\n", u.Id, u.Name)
	}

	fmt.Println("client demo OK")
}

func runUnary(ctx context.Context, c usersv1.UsersServiceHTTPClient) {
	// Create a user.
	created, err := c.CreateUser(ctx, &usersv1.CreateUserRequest{Name: "SDK User", Email: "sdk@example.com"})
	if err != nil {
		log.Fatalf("CreateUser: %v", err)
	}
	fmt.Printf("CreateUser  -> id=%s name=%s\n", created.Id, created.Name)

	// Get it back — requires auth (JWT-style via header).
	got, err := c.GetUser(ctx, &usersv1.GetUserRequest{Id: created.Id},
		client.WithHeader(metadata.HeaderAuthorization, "Bearer secret-token-123"),
		client.WithHeader("x-trace", "demo"),
	)
	if err != nil {
		log.Fatalf("GetUser: %v", err)
	}
	fmt.Printf("GetUser     -> id=%s name=%s email=%s\n", got.Id, got.Name, got.Email)

	// Without auth, GetUser is rejected (demonstrates header auth).
	_, err = c.GetUser(ctx, &usersv1.GetUserRequest{Id: created.Id})
	if err != nil {
		fmt.Printf("GetUser (no auth) -> %v (expected)\n", err)
	} else {
		fmt.Printf("GetUser (no auth) -> unexpected success\n")
	}

	// Echo via the additional GET binding.
	echo, err := c.Echo(ctx, &usersv1.EchoRequest{Message: "hello sdk"})
	if err != nil {
		log.Fatalf("Echo: %v", err)
	}
	fmt.Printf("Echo        -> %q\n", echo.Message)
}

func runStreaming(ctx context.Context, c usersv1.UsersServiceHTTPClient) {
	// Server streaming (SSE).
	watch, err := c.WatchUsers(ctx, &usersv1.WatchUsersRequest{IntervalSeconds: 1})
	if err != nil {
		log.Fatalf("WatchUsers: %v", err)
	}
	defer watch.Close()
	fmt.Println("WatchUsers  -> (SSE, first 2 events)")
	for i := 0; i < 2; i++ {
		u, err := watch.Receive()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("WatchUsers recv: %v", err)
		}
		fmt.Printf("  event: id=%s name=%s\n", u.Id, u.Name)
	}

	// Client streaming (NDJSON).
	up, err := c.UploadUsers(ctx)
	if err != nil {
		log.Fatalf("UploadUsers: %v", err)
	}
	for _, name := range []string{"Stream A", "Stream B", "Stream C"} {
		if err := up.Send(&usersv1.User{Name: name}); err != nil {
			log.Fatalf("UploadUsers send: %v", err)
		}
	}
	resp, err := up.CloseAndReceive()
	if err != nil {
		log.Fatalf("UploadUsers close: %v", err)
	}
	fmt.Printf("UploadUsers -> stored %d users\n", resp.Count)

	// Bidi streaming (WebSocket).
	chat, err := c.Chat(ctx)
	if err != nil {
		log.Fatalf("Chat: %v", err)
	}
	defer chat.Close()
	for _, text := range []string{"ping", "pong"} {
		if err := chat.Send(&usersv1.ChatMessage{User: "sdk", Text: text}); err != nil {
			log.Fatalf("Chat send: %v", err)
		}
		msg, err := chat.Receive()
		if err != nil {
			log.Fatalf("Chat recv: %v", err)
		}
		fmt.Printf("Chat        -> %q echoed as %q\n", text, msg.Text)
	}
}
