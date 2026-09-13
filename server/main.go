// Command server runs the example as a combined gRPC + HTTP server on a
// single port.
//
//   - gRPC: users, todos, and auth over HTTP/2 cleartext (h2c).
//   - HTTP: REST (unary), SSE (server streaming), NDJSON (client
//     streaming), and WebSocket (bidi) served by the zararpc server.
//
// Both protocols share one listener on :8080.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	todosv1 "github.com/aldok10/zara-rpc-examples/proto/todos/v1"
	usersv1 "github.com/aldok10/zara-rpc-examples/proto/users/v1"
	"github.com/aldok10/zara-rpc/auth/jwt"
	"github.com/aldok10/zara-rpc/routing"
)

// getOrGenerateSecret returns the JWT signing secret. It reads JWT_SECRET
// first (so the demo client can share a pinned key when needed) and falls
// back to a fresh cryptographically random 32-byte key per process — no
// literal in the source, so secret scanners never fire a false positive.
// The client demo gets its tokens through the auth service (register +
// login), so a random secret works out of the box.
func getOrGenerateSecret() []byte {
	if s := os.Getenv("JWT_SECRET"); s != "" {
		return []byte(s)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("generate jwt secret: %v", err)
	}
	return []byte(hex.EncodeToString(b))
}

func main() {
	dbPath := os.Getenv("TODO_DB")
	if dbPath == "" {
		dbPath = "todo.db"
	}
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&todosv1.Todo{}, &usersv1.UserRow{}); err != nil {
		log.Fatalf("auto migrate: %v", err)
	}

	secret := getOrGenerateSecret()

	users := &usersService{db: db}
	todos := &todoService{db: db}
	authSvc := &authService{db: db, issuer: jwt.NewIssuer(secret)}

	unaryChain, streamChain, err := authChains(secret)
	if err != nil {
		log.Fatalf("auth chain: %v", err)
	}

	// Register services on a *routing.Server (unified HTTP & gRPC). The
	// interceptors are registered once and guard every transport: HTTP
	// unary, HTTP streams (SSE/NDJSON/WebSocket), and gRPC (the gRPC
	// server is enabled by default via grpcbridge's factory).
	server := routing.NewServer(
		routing.WithUnaryInterceptors(unaryChain...),
		routing.WithStreamInterceptors(streamChain...),
	)

	if err := usersv1.RegisterUsersServiceHandler(server, users); err != nil {
		log.Fatalf("register users service: %v", err)
	}
	if err := todosv1.RegisterTodoServiceHandler(server, todos); err != nil {
		log.Fatalf("register todo service: %v", err)
	}
	if err := usersv1.RegisterAuthServiceHandler(server, authSvc); err != nil {
		log.Fatalf("register auth service: %v", err)
	}

	addr := "localhost:8080"
	log.Printf("Secret Key JWT is %s", string(secret))
	log.Printf("combined gRPC + HTTP server listening on http://%s", addr)
	log.Printf("  REST:     curl http://%s/v1/users", addr)
	log.Printf("  register: curl -d '{\"username\":\"alice\",\"password\":\"secret\"}' http://%s/v1/auth/register", addr)
	log.Printf("  login:    curl -d '{\"username\":\"alice\",\"password\":\"secret\"}' http://%s/v1/auth/login", addr)

	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	s := &http.Server{
		Addr:      addr,
		Handler:   server,
		Protocols: p,
	}
	if err := s.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
