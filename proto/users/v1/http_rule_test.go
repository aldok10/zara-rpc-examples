package usersv1

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	routing "github.com/aldok10/zara-rpc/routing"
	"github.com/aldok10/zara-rpc/runtime"
)

// testService implements UsersServiceHandler with minimal behavior for the
// HTTP rule tests. UnimplementedUsersServiceHandler provides the rest.
type testService struct {
	UnimplementedUsersServiceHandler
	echoCalls int
}

func (s *testService) Echo(ctx runtime.Ctx, req *EchoRequest) (*EchoResponse, error) {
	s.echoCalls++
	return &EchoResponse{Message: req.Message}, nil
}

func (s *testService) GetUserProfile(ctx runtime.Ctx, req *GetUserRequest) (*User, error) {
	return &User{Id: req.Id, Name: "alice", Email: "alice@example.com"}, nil
}

func (s *testService) ActivateUser(ctx runtime.Ctx, req *ActivateUserRequest) (*User, error) {
	return &User{Id: req.Name, Name: req.Name, Email: req.Name + "@example.com"}, nil
}

// newTestMux registers the service routes on a fresh mux and serves them
// over httptest.
func newTestMux(t *testing.T, svc UsersServiceHandler) *httptest.Server {
	t.Helper()
	mux := routing.NewMux()
	if err := RegisterUsersServiceRoutes(mux, svc); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestResponseBody proves the response_body annotation: the HTTP body is the
// selected field (the name), not the full User message.
func TestResponseBody(t *testing.T) {
	srv := newTestMux(t, &testService{})

	resp, err := http.Get(srv.URL + "/v1/users/1/profile")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/users/1/profile status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), `"alice"`; got != want {
		t.Errorf("response body = %s, want %s (response_body selects the name field)", got, want)
	}
}

// TestAdditionalBindings proves both the primary and the additional binding
// route to the same handler: POST /v1/echo and GET /v1/echo/{message}.
func TestAdditionalBindings(t *testing.T) {
	svc := &testService{}
	srv := newTestMux(t, svc)

	// Primary binding: POST /v1/echo with a JSON body.
	resp, err := http.Post(srv.URL+"/v1/echo", "application/json", strings.NewReader(`{"message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/echo status = %d, want 200", resp.StatusCode)
	}

	// Additional binding: GET /v1/echo/{message}.
	resp, err = http.Get(srv.URL + "/v1/echo/hi")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/echo/hi status = %d, want 200", resp.StatusCode)
	}

	if got, want := svc.echoCalls, 2; got != want {
		t.Errorf("Echo handler calls = %d, want %d (both bindings route to the same handler)", got, want)
	}
}

// TestCustomVerb proves POST /v1/users/{name}:activate extracts the path
// param and invokes the handler with name=alice.
func TestCustomVerb(t *testing.T) {
	srv := newTestMux(t, &testService{})

	resp, err := http.Post(srv.URL+"/v1/users/alice:activate", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/users/alice:activate status = %d, want 200", resp.StatusCode)
	}
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Id != "alice" {
		t.Errorf("handler received name = %q, want %q (path param extracted)", u.Id, "alice")
	}
}
