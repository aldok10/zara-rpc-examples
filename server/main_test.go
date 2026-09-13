package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	todosv1 "github.com/aldok10/zara-rpc-examples/proto/todos/v1"
	"github.com/aldok10/zara-rpc/routing"
)

// mintTestToken signs a demo HS256 JWT with the same secret as the server
// (authChains in main.go) so the test exercises the real auth stack.
func mintTestToken(secret []byte, sub, role string) string {
	claims := jwt.MapClaims{
		"sub":  sub,
		"role": role,
		"exp":  time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		panic(err)
	}
	return signed
}

// newTodoTestServer builds a mux with the real auth chain (JWT + RBAC) and
// a TodoService backed by an in-memory SQLite database. It returns the
// random JWT secret so tests can mint tokens with it.
func newTodoTestServer(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&todosv1.Todo{}); err != nil {
		t.Fatal(err)
	}
	secret := getOrGenerateSecret()
	unaryChain, _, err := authChains(secret)
	if err != nil {
		t.Fatal(err)
	}
	mux := routing.NewMux(routing.WithMuxUnaryInterceptors(unaryChain...))
	if err := todosv1.RegisterTodoServiceRoutes(mux, &todoService{db: db}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, secret
}

// doJSON performs an HTTP request with an optional bearer token and JSON
// body, returning the response and its body.
func doJSON(t *testing.T, method, url, token, body string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

// TestTodoCRUD exercises create → list → get → update → delete through the
// mux with minted JWTs, and asserts owner scoping: bob never sees alice's
// todos.
func TestTodoCRUD(t *testing.T) {
	srv, secret := newTodoTestServer(t)
	alice := "Bearer " + mintTestToken(secret, "alice", "user")
	bob := "Bearer " + mintTestToken(secret, "bob", "user")

	// Create alice's todo.
	resp, body := doJSON(t, http.MethodPost, srv.URL+"/v1/todos", alice, `{"title":"buy milk"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, body=%s", resp.StatusCode, body)
	}
	var created todosv1.Todo
	if err := protojson.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.Id == "" {
		t.Error("id is empty, want server-generated UUID")
	}
	if created.OwnerId != "alice" {
		t.Errorf("owner_id = %q, want alice (from claims, not the body)", created.OwnerId)
	}
	if created.Done {
		t.Error("done = true, want false")
	}
	if created.CreatedAt == 0 || created.UpdatedAt == 0 {
		t.Errorf("timestamps not set: created_at=%d updated_at=%d", created.CreatedAt, created.UpdatedAt)
	}

	// List as alice — contains the todo.
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/todos", alice, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", resp.StatusCode, body)
	}
	var list todosv1.ListTodosResponse
	if err := protojson.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Todos) != 1 || list.Todos[0].Id != created.Id {
		t.Errorf("list = %d todos, want 1 with id %s", len(list.Todos), created.Id)
	}

	// List as bob — empty (owner scoping).
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/todos", bob, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bob list status = %d, body=%s", resp.StatusCode, body)
	}
	var bobList todosv1.ListTodosResponse
	if err := protojson.Unmarshal(body, &bobList); err != nil {
		t.Fatal(err)
	}
	if len(bobList.Todos) != 0 {
		t.Errorf("bob sees %d todos, want 0 (owner scoping)", len(bobList.Todos))
	}

	// Get alice's todo as bob — 404, not 200.
	resp, _ = doJSON(t, http.MethodGet, srv.URL+"/v1/todos/"+created.Id, bob, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("bob get status = %d, want 404 (owner scoping)", resp.StatusCode)
	}

	// Update as alice — mark done, title preserved when omitted.
	resp, body = doJSON(t, http.MethodPut, srv.URL+"/v1/todos/"+created.Id, alice, `{"done":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", resp.StatusCode, body)
	}
	var updated todosv1.Todo
	if err := protojson.Unmarshal(body, &updated); err != nil {
		t.Fatal(err)
	}
	if !updated.Done {
		t.Error("done = false, want true after update")
	}
	if updated.Title != "buy milk" {
		t.Errorf("title = %q, want buy milk (title preserved when omitted)", updated.Title)
	}

	// Delete as alice.
	resp, _ = doJSON(t, http.MethodDelete, srv.URL+"/v1/todos/"+created.Id, alice, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}

	// Gone from the list.
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/todos", alice, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list after delete status = %d", resp.StatusCode)
	}
	var after todosv1.ListTodosResponse
	if err := protojson.Unmarshal(body, &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Todos) != 0 {
		t.Errorf("after delete: %d todos, want 0", len(after.Todos))
	}
}

// TestTodoRequiresAuth proves the JWT interceptor rejects requests without
// a token before the handler runs.
func TestTodoRequiresAuth(t *testing.T) {
	srv, _ := newTodoTestServer(t)
	resp, body := doJSON(t, http.MethodPost, srv.URL+"/v1/todos", "", `{"title":"nope"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, body)
	}
}
