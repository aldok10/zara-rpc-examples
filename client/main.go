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
	"net/http"
	"time"

	todosv1 "github.com/aldok10/zara-rpc-examples/proto/todos/v1"
	usersv1 "github.com/aldok10/zara-rpc-examples/proto/users/v1"
	"github.com/aldok10/zara-rpc/client"
	"github.com/aldok10/zara-rpc/codes"
	"github.com/aldok10/zara-rpc/encoding"
	"github.com/aldok10/zara-rpc/metadata"
	"github.com/aldok10/zara-rpc/status"
)

// demoTokens holds bearer tokens for the demo roles. The server signs JWTs
// with a random secret per process (examples/server getOrGenerateSecret), so
// tokens cannot be minted locally: they come from the auth service itself
// (register + login).
var demoTokens struct{ admin, user string }

// loginRole registers username (tolerating "already exists" from previous
// runs) and logs in, returning the issued JWT.
func loginRole(ctx context.Context, auth usersv1.AuthServiceHTTPClient, username, role string) string {
	const password = "demo-password"
	_, err := auth.Register(ctx, &usersv1.RegisterRequest{
		Username: username, Password: password, Email: username + "@example.com", Role: role,
	})
	if err != nil && status.Code(err) != codes.CodeAlreadyExists {
		log.Fatalf("register %s: %v", username, err)
	}
	resp, err := auth.Login(ctx, &usersv1.LoginRequest{Username: username, Password: password})
	if err != nil {
		log.Fatalf("login %s: %v", username, err)
	}
	return resp.Token
}

// bearer returns an Authorization header value for a role ("admin" or
// "user"), minted through the auth service at startup.
func bearer(role string) string {
	switch role {
	case "admin":
		return "Bearer " + demoTokens.admin
	default:
		return "Bearer " + demoTokens.user
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	baseURL := "http://localhost:8080"

	// ---- Obtain role tokens through the auth service ----
	// The server's signing secret is random per process, so the demo cannot
	// forge JWTs locally; it registers two demo users (admin + reader) and
	// logs in for real tokens. Re-runs against a persistent DB tolerate the
	// 409 already-exists.
	auth := usersv1.NewAuthServiceHTTPClient(baseURL)
	demoTokens.admin = loginRole(ctx, auth, "demo-admin", "admin")
	demoTokens.user = loginRole(ctx, auth, "demo-reader", "user")
	fmt.Println("== Auth ==\nTokens    -> obtained via register + login (admin, reader)")

	// ---- JSON client (default codec) ----
	jsonClient := usersv1.NewUsersServiceHTTPClient(baseURL)
	fmt.Println("== JSON client ==")
	runUnary(ctx, baseURL, jsonClient)

	// ---- Protobuf client (binary codec) ----
	protoClient := usersv1.NewUsersServiceHTTPClient(baseURL, client.WithCodec(encoding.ProtoCodec{}))
	fmt.Println("== Protobuf client ==")
	runUnary(ctx, baseURL, protoClient)

	// ---- Todos (GORM + SQLite) ----
	fmt.Println("== Todos (GORM + SQLite) ==")
	runTodos(ctx, baseURL)

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

func runUnary(ctx context.Context, baseURL string, c usersv1.UsersServiceHTTPClient) {
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

	// GetUserByID exercises automatic query binding: the request has no path
	// params and no body, so the id field binds from the query string
	// (GET /v1/users/getByID?id=...).
	byID, err := c.GetUserByID(ctx, &usersv1.GetUserRequest{Id: created.Id},
		client.WithHeader(metadata.HeaderAuthorization, bearer("user")),
	)
	if err != nil {
		log.Fatalf("GetUserByID: %v", err)
	}
	fmt.Printf("GetUserByID -> id=%s name=%s (query binding, reader token)\n", byID.Id, byID.Name)

	// ActivateUser via the custom verb route POST /v1/users/{name}:activate
	// — admin only (not in the readers allow rule).
	activated, err := c.ActivateUser(ctx, &usersv1.ActivateUserRequest{Name: created.Name},
		client.WithHeader(metadata.HeaderAuthorization, bearer("admin")),
	)
	if err != nil {
		log.Fatalf("ActivateUser: %v", err)
	}
	fmt.Printf("ActivateUser -> id=%s name=%s (admin token, custom verb)\n", activated.Id, activated.Name)

	// GetUserProfile uses response_body: the HTTP body is the selected field
	// only (the name), not the full User message. The typed client would try
	// to unmarshal the field into User, so this call uses net/http directly.
	profileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/users/"+created.Id+"/profile", nil)
	if err != nil {
		log.Fatalf("GetUserProfile request: %v", err)
	}
	profileReq.Header.Set(metadata.HeaderAuthorization, bearer("admin"))
	profileResp, err := http.DefaultClient.Do(profileReq)
	if err != nil {
		log.Fatalf("GetUserProfile: %v", err)
	}
	profileBody, readErr := io.ReadAll(profileResp.Body)
	profileResp.Body.Close()
	if readErr != nil {
		log.Fatalf("GetUserProfile read: %v", readErr)
	}
	fmt.Printf("GetUserProfile -> body=%s (response_body: name)\n", profileBody)

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

// runTodos exercises the TodoService (GORM + SQLite) with the typed
// client: create → list → mark done → delete. The user role is allowed by
// the RBAC policy (todo-users allow rule).
func runTodos(ctx context.Context, baseURL string) {
	c := todosv1.NewTodoServiceHTTPClient(baseURL)

	created, err := c.CreateTodo(ctx, &todosv1.CreateTodoRequest{Title: "buy milk"},
		client.WithHeader(metadata.HeaderAuthorization, bearer("user")),
	)
	if err != nil {
		log.Fatalf("CreateTodo: %v", err)
	}
	fmt.Printf("CreateTodo  -> id=%s title=%q owner=%s done=%v (user token)\n", created.Id, created.Title, created.OwnerId, created.Done)

	listed, err := c.ListTodos(ctx, &todosv1.ListTodosRequest{},
		client.WithHeader(metadata.HeaderAuthorization, bearer("user")),
	)
	if err != nil {
		log.Fatalf("ListTodos: %v", err)
	}
	fmt.Printf("ListTodos   -> %d todo(s)\n", len(listed.Todos))

	updated, err := c.UpdateTodo(ctx, &todosv1.UpdateTodoRequest{Id: created.Id, Done: true},
		client.WithHeader(metadata.HeaderAuthorization, bearer("user")),
	)
	if err != nil {
		log.Fatalf("UpdateTodo: %v", err)
	}
	fmt.Printf("UpdateTodo  -> id=%s done=%v (user token)\n", updated.Id, updated.Done)

	if _, err := c.DeleteTodo(ctx, &todosv1.DeleteTodoRequest{Id: created.Id},
		client.WithHeader(metadata.HeaderAuthorization, bearer("user")),
	); err != nil {
		log.Fatalf("DeleteTodo: %v", err)
	}
	fmt.Println("DeleteTodo  -> deleted (user token)")
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
	for i, name := range []string{"Stream A", "Stream B", "Stream C"} {
		if err := up.Send(&usersv1.User{Name: name, Email: fmt.Sprintf("stream-%d@example.com", i+1)}); err != nil {
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
