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

	"github.com/golang-jwt/jwt/v5"

	usersv1 "github.com/aldok10/zara-rpc-examples/proto/users/v1"
	"github.com/aldok10/zara-rpc/client"
	"github.com/aldok10/zara-rpc/encoding"
	"github.com/aldok10/zara-rpc/metadata"
)

// demoSecret matches the server's JWT signing secret (examples/server).
const demoSecret = "secret-token-123"

// mintToken signs a demo HS256 JWT with the given role claim. The server
// validates it with auth.NewJWTValidator and enforces the RBAC policy on
// the role.
func mintToken(role string) string {
	claims := jwt.MapClaims{
		"sub":  "users/1",
		"role": role,
		"exp":  time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(demoSecret))
	if err != nil {
		log.Fatalf("mint token: %v", err)
	}
	return signed
}

// bearer returns an Authorization header value for a role.
func bearer(role string) string {
	return "Bearer " + mintToken(role)
}

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
	watchWS, err := wsClient.WatchUsers(ctx, &usersv1.WatchUsersRequest{IntervalSeconds: 1},
		client.WithHeader(metadata.HeaderAuthorization, bearer("admin")),
	)
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

	// A reader token is rejected on WatchUsers (not in the readers allow
	// rule) — demonstrates authz on streaming endpoints.
	watchDenied, err := wsClient.WatchUsers(ctx, &usersv1.WatchUsersRequest{IntervalSeconds: 1},
		client.WithHeader(metadata.HeaderAuthorization, bearer("user")),
	)
	if err == nil {
		_, err = watchDenied.Receive()
		watchDenied.Close()
	}
	if err != nil {
		fmt.Printf("WatchUsers (reader) -> %v (expected 403)\n", err)
	} else {
		fmt.Println("WatchUsers (reader) -> unexpected success")
	}

	fmt.Println("client demo OK")
}

func runUnary(ctx context.Context, c usersv1.UsersServiceHTTPClient) {
	// Create a user — requires the admin role (admins allow rule).
	created, err := c.CreateUser(ctx, &usersv1.CreateUserRequest{Name: "SDK User", Email: "sdk@example.com"},
		client.WithHeader(metadata.HeaderAuthorization, bearer("admin")),
	)
	if err != nil {
		log.Fatalf("CreateUser: %v", err)
	}
	fmt.Printf("CreateUser  -> id=%s name=%s (admin token)\n", created.Id, created.Name)

	// Get it back — any authenticated caller may read (readers allow rule).
	got, err := c.GetUser(ctx, &usersv1.GetUserRequest{Id: created.Id},
		client.WithHeader(metadata.HeaderAuthorization, bearer("user")),
		client.WithHeader("x-trace", "demo"),
	)
	if err != nil {
		log.Fatalf("GetUser: %v", err)
	}
	fmt.Printf("GetUser     -> id=%s name=%s email=%s (reader token)\n", got.Id, got.Name, got.Email)

	// Without a token, the JWT interceptor rejects the request (401).
	_, err = c.GetUser(ctx, &usersv1.GetUserRequest{Id: created.Id})
	if err != nil {
		fmt.Printf("GetUser (no auth) -> %v (expected 401)\n", err)
	} else {
		fmt.Printf("GetUser (no auth) -> unexpected success\n")
	}

	// A reader is denied DeleteUser by the deny rule (403).
	_, err = c.DeleteUser(ctx, &usersv1.DeleteUserRequest{Id: created.Id},
		client.WithHeader(metadata.HeaderAuthorization, bearer("user")),
	)
	if err != nil {
		fmt.Printf("DeleteUser (reader) -> %v (expected 403)\n", err)
	} else {
		fmt.Printf("DeleteUser (reader) -> unexpected success\n")
	}

	// An admin may delete (deny rule only matches role=user).
	if _, err := c.DeleteUser(ctx, &usersv1.DeleteUserRequest{Id: created.Id},
		client.WithHeader(metadata.HeaderAuthorization, bearer("admin")),
	); err != nil {
		log.Fatalf("DeleteUser (admin): %v", err)
	}
	fmt.Println("DeleteUser  -> deleted (admin token)")

	// Echo via the additional GET binding — allowed for any authenticated
	// caller (readers allow rule).
	echo, err := c.Echo(ctx, &usersv1.EchoRequest{Message: "hello sdk"},
		client.WithHeader(metadata.HeaderAuthorization, bearer("user")),
	)
	if err != nil {
		log.Fatalf("Echo: %v", err)
	}
	fmt.Printf("Echo        -> %q (reader token)\n", echo.Message)
}

func runStreaming(ctx context.Context, c usersv1.UsersServiceHTTPClient) {
	// Seed a user so WatchUsers has events to stream (it only streams
	// existing users; the unary demo deleted its own).
	if _, err := c.CreateUser(ctx, &usersv1.CreateUserRequest{Name: "Stream Seed", Email: "seed@example.com"},
		client.WithHeader(metadata.HeaderAuthorization, bearer("admin")),
	); err != nil {
		log.Fatalf("CreateUser (seed): %v", err)
	}

	// Server streaming (SSE) — admin token (WatchUsers is not in the
	// readers allow rule).
	watch, err := c.WatchUsers(ctx, &usersv1.WatchUsersRequest{IntervalSeconds: 1},
		client.WithHeader(metadata.HeaderAuthorization, bearer("admin")),
	)
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

	// Client streaming (NDJSON) — admin token.
	up, err := c.UploadUsers(ctx, client.WithHeader(metadata.HeaderAuthorization, bearer("admin")))
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

	// Bidi streaming (WebSocket) — admin token.
	chat, err := c.Chat(ctx, client.WithHeader(metadata.HeaderAuthorization, bearer("admin")))
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
