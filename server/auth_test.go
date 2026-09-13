package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	usersv1 "github.com/aldok10/zara-rpc-examples/proto/users/v1"
	"github.com/aldok10/zara-rpc/auth/jwt"
	"github.com/aldok10/zara-rpc/routing"
)

// jsonUnmarshal decodes a JSON response body into dst.
func jsonUnmarshal(data []byte, dst any) error {
	return json.Unmarshal(data, dst)
}

// newAuthTestServer builds ONE mux with the real auth chain, mirroring
// main(): the auth service (register + login) is registered on the same mux
// as the protected services, and the chain's Public interceptor marks
// /v1/auth/* public so JWT/RBAC skip those routes. The JWT secret is the
// demo secret, exactly like the server.
func newAuthTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&usersv1.UserRow{}); err != nil {
		t.Fatal(err)
	}
	secret := getOrGenerateSecret()
	authSvc := &authService{db: db, issuer: jwt.NewIssuer(secret)}

	unaryChain, _, err := authChains(secret)
	if err != nil {
		t.Fatal(err)
	}
	mux := routing.NewMux(routing.WithMuxUnaryInterceptors(unaryChain...))
	if err := usersv1.RegisterAuthServiceRoutes(mux, authSvc); err != nil {
		t.Fatal(err)
	}
	if err := usersv1.RegisterUsersServiceRoutes(mux, &usersService{db: db}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestAuthRegisterLogin exercises register → login → protected endpoint
// through the real single mux: a registered user gets a JWT from login and
// can call a protected RPC with it. Register/login work without a token
// because the chain's Public interceptor marks /v1/auth/* public.
func TestAuthRegisterLogin(t *testing.T) {
	srv := newAuthTestServer(t)

	// Register a new user (JSON body) — public, no token.
	resp, body := doJSON(t, http.MethodPost, srv.URL+"/v1/auth/register", "",
		`{"username":"alice","password":"secret","email":"alice@example.com"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", resp.StatusCode, body)
	}
	var reg usersv1.RegisterResponse
	if err := jsonUnmarshal(body, &reg); err != nil {
		t.Fatal(err)
	}
	if reg.Id == "" || reg.Username != "alice" {
		t.Errorf("register response = %+v, want id + username alice", &reg)
	}

	// Duplicate username is rejected.
	resp, body = doJSON(t, http.MethodPost, srv.URL+"/v1/auth/register", "",
		`{"username":"alice","password":"other"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate register status = %d, want 409; body=%s", resp.StatusCode, body)
	}

	// Login with the wrong password is rejected.
	resp, body = doJSON(t, http.MethodPost, srv.URL+"/v1/auth/login", "",
		`{"username":"alice","password":"wrong"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad login status = %d, want 401; body=%s", resp.StatusCode, body)
	}

	// Login with the right password returns a JWT.
	resp, body = doJSON(t, http.MethodPost, srv.URL+"/v1/auth/login", "",
		`{"username":"alice","password":"secret"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", resp.StatusCode, body)
	}
	var login usersv1.LoginResponse
	if err := jsonUnmarshal(body, &login); err != nil {
		t.Fatal(err)
	}
	if login.Token == "" {
		t.Fatal("login returned empty token")
	}
	if login.User == nil || login.User.Name != "alice" {
		t.Errorf("login user = %+v, want alice", login.User)
	}

	// The token authenticates against the same mux.
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/users/"+reg.Id, "Bearer "+login.Token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("protected get status = %d, body=%s", resp.StatusCode, body)
	}
	var user usersv1.User
	if err := jsonUnmarshal(body, &user); err != nil {
		t.Fatal(err)
	}
	if user.Name != "alice" {
		t.Errorf("user name = %q, want alice", user.Name)
	}

	// Without a token the same mux rejects protected routes.
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/users/"+reg.Id, "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-token status = %d, want 401; body=%s", resp.StatusCode, body)
	}
}

// TestAuthGetUserByIDQuery exercises the automatic query binding on the
// protected mux: GET /v1/users/getByID?id=<id> binds the id from the query
// string (no path param, no body), and the route still requires a token.
func TestAuthGetUserByIDQuery(t *testing.T) {
	srv := newAuthTestServer(t)

	// Register + login to get a token.
	resp, body := doJSON(t, http.MethodPost, srv.URL+"/v1/auth/register", "",
		`{"username":"bob","password":"secret","email":"bob@example.com"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", resp.StatusCode, body)
	}
	var reg usersv1.RegisterResponse
	if err := jsonUnmarshal(body, &reg); err != nil {
		t.Fatal(err)
	}
	resp, body = doJSON(t, http.MethodPost, srv.URL+"/v1/auth/login", "",
		`{"username":"bob","password":"secret"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", resp.StatusCode, body)
	}
	var login usersv1.LoginResponse
	if err := jsonUnmarshal(body, &login); err != nil {
		t.Fatal(err)
	}

	// Query binding: ?id=<uuid> → GetUserRequest{id}.
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/users/getByID?id="+reg.Id, "Bearer "+login.Token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getByID status = %d, body=%s", resp.StatusCode, body)
	}
	var user usersv1.User
	if err := jsonUnmarshal(body, &user); err != nil {
		t.Fatal(err)
	}
	if user.Id != reg.Id || user.Name != "bob" {
		t.Errorf("getByID user = %+v, want id=%s name=bob", &user, reg.Id)
	}

	// Unknown id → 404.
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/users/getByID?id=missing", "Bearer "+login.Token, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("getByID missing status = %d, want 404; body=%s", resp.StatusCode, body)
	}

	// No token → 401.
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/users/getByID?id="+reg.Id, "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("getByID no-token status = %d, want 401; body=%s", resp.StatusCode, body)
	}
}