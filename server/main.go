// Command server runs the example UsersService as a combined gRPC + HTTP
// server on a single port.
//
//   - gRPC:   served over HTTP/2 cleartext (h2c), with reflection enabled so
//     grpcurl can discover the schema without a .proto file.
//   - HTTP:   REST endpoints (unary), SSE (server streaming), NDJSON (client
//     streaming), and WebSocket (bidi) served by the zararpc mux.
//
// Both protocols share one listener on :8080. Requests with
// content-type: application/grpc are routed to the gRPC server; everything
// else goes to the HTTP mux.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/aldok10/zara-rpc/codes"
	usersv1 "github.com/aldok10/zara-rpc/examples/proto/users/v1"
	"github.com/aldok10/zara-rpc/metadata"
	"github.com/aldok10/zara-rpc/runtime"
	"github.com/aldok10/zara-rpc/status"
)

// authToken extracts a bearer token from the Authorization header, the
// "token" query parameter, or the "session" cookie, in that order. This
// shows how handlers read transport payloads uniformly over HTTP and gRPC.
func authToken(ctx context.Context) string {
	if h := metadata.HeaderFromContext(ctx).Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if t := metadata.QueryFromContext(ctx).Get("token"); t != "" {
		return t
	}
	if c, err := metadata.CookieFromContext(ctx, "session"); err == nil {
		return c.Value
	}
	return ""
}

// requireAuth rejects requests without a valid token.
func requireAuth(ctx context.Context) error {
	if authToken(ctx) != "secret-token-123" {
		return status.NewErrorf(codes.CodeUnauthenticated, "missing or invalid auth token")
	}
	return nil
}

// withGRPCMeta converts incoming gRPC metadata into RequestMeta so
// handlers see the same header API over both transports.
func withGRPCMeta(ctx context.Context) context.Context {
	md, _ := grpcmetadata.FromIncomingContext(ctx)
	header := make(http.Header)
	for k, vs := range md {
		for _, v := range vs {
			header.Add(k, v)
		}
	}
	return metadata.WithRequestMeta(ctx, metadata.RequestMeta{Header: header})
}

// usersService implements usersv1.UsersServiceHandler (the zararpc HTTP
// contract). The same struct is adapted to the gRPC contract below.
type usersService struct {
	usersv1.UnimplementedUsersServiceHandler
	mu    sync.Mutex
	users map[string]*usersv1.User
	next  int64
}

func (s *usersService) GetUser(ctx context.Context, req *usersv1.GetUserRequest) (*usersv1.User, error) {
	// Auth: reads the token from header, query param, or cookie — same
	// code path regardless of whether the request came via HTTP or gRPC.
	if err := requireAuth(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[req.Id]
	if !ok {
		return nil, status.NewErrorf(codes.CodeNotFound, "user %q not found", req.Id)
	}
	return u, nil
}

func (s *usersService) ListUsers(ctx context.Context, req *usersv1.ListUsersRequest) (*usersv1.ListUsersResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp := &usersv1.ListUsersResponse{}
	for _, u := range s.users {
		resp.Users = append(resp.Users, u)
	}
	return resp, nil
}

func (s *usersService) CreateUser(ctx context.Context, req *usersv1.CreateUserRequest) (*usersv1.User, error) {
	// Demo: read the raw body and a cookie from the transport payload.
	body := metadata.BodyFromContext(ctx)
	if c, err := metadata.CookieFromContext(ctx, "session"); err == nil {
		log.Printf("CreateUser: session cookie=%q body=%d bytes", c.Value, len(body))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	u := &usersv1.User{Id: fmt.Sprintf("%d", s.next), Name: req.Name, Email: req.Email}
	s.users[u.Id] = u
	return u, nil
}

func (s *usersService) UpdateUser(ctx context.Context, req *usersv1.UpdateUserRequest) (*usersv1.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[req.Id]
	if !ok {
		return nil, status.NewErrorf(codes.CodeNotFound, "user %q not found", req.Id)
	}
	if req.Name != "" {
		u.Name = req.Name
	}
	if req.Email != "" {
		u.Email = req.Email
	}
	return u, nil
}

func (s *usersService) DeleteUser(ctx context.Context, req *usersv1.DeleteUserRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.users, req.Id)
	return &emptypb.Empty{}, nil
}

func (s *usersService) Echo(ctx context.Context, req *usersv1.EchoRequest) (*usersv1.EchoResponse, error) {
	return &usersv1.EchoResponse{Message: req.Message}, nil
}

// WatchUsers streams all users every interval_seconds (SSE transport).
func (s *usersService) WatchUsers(ctx context.Context, req *usersv1.WatchUsersRequest, stream runtime.ServerStream[*usersv1.User]) error {
	interval := time.Duration(req.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.mu.Lock()
			for _, u := range s.users {
				if err := stream.Send(u); err != nil {
					s.mu.Unlock()
					return err
				}
			}
			s.mu.Unlock()
		}
	}
}

// UploadUsers reads a stream of users and stores them (NDJSON transport).
func (s *usersService) UploadUsers(ctx context.Context, stream runtime.ClientStream[*usersv1.User]) (*usersv1.UploadUsersResponse, error) {
	var count int32
	for {
		u, err := stream.Receive()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.next++
		u.Id = fmt.Sprintf("%d", s.next)
		s.users[u.Id] = u
		s.mu.Unlock()
		count++
	}
	return &usersv1.UploadUsersResponse{Count: count}, nil
}

// Chat echoes messages back (WebSocket transport).
func (s *usersService) Chat(ctx context.Context, stream runtime.BidiStream[*usersv1.ChatMessage, *usersv1.ChatMessage]) error {
	for {
		msg, err := stream.Receive()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&usersv1.ChatMessage{User: msg.User, Text: "echo: " + msg.Text}); err != nil {
			return err
		}
	}
}

// ---- gRPC adapter ----

// grpcUsersService adapts usersService to the grpc-go contract
// (usersv1.UsersServiceServer). Unary methods delegate directly; streaming
// methods convert between grpc stream types and zararpc stream types.
type grpcUsersService struct {
	*usersService
	usersv1.UnimplementedUsersServiceServer
}

func (g *grpcUsersService) GetUser(ctx context.Context, req *usersv1.GetUserRequest) (*usersv1.User, error) {
	return g.usersService.GetUser(withGRPCMeta(ctx), req)
}

func (g *grpcUsersService) ListUsers(ctx context.Context, req *usersv1.ListUsersRequest) (*usersv1.ListUsersResponse, error) {
	return g.usersService.ListUsers(withGRPCMeta(ctx), req)
}

func (g *grpcUsersService) CreateUser(ctx context.Context, req *usersv1.CreateUserRequest) (*usersv1.User, error) {
	return g.usersService.CreateUser(withGRPCMeta(ctx), req)
}

func (g *grpcUsersService) UpdateUser(ctx context.Context, req *usersv1.UpdateUserRequest) (*usersv1.User, error) {
	return g.usersService.UpdateUser(withGRPCMeta(ctx), req)
}

func (g *grpcUsersService) DeleteUser(ctx context.Context, req *usersv1.DeleteUserRequest) (*emptypb.Empty, error) {
	return g.usersService.DeleteUser(withGRPCMeta(ctx), req)
}

func (g *grpcUsersService) Echo(ctx context.Context, req *usersv1.EchoRequest) (*usersv1.EchoResponse, error) {
	return g.usersService.Echo(withGRPCMeta(ctx), req)
}

func (g *grpcUsersService) WatchUsers(req *usersv1.WatchUsersRequest, stream grpc.ServerStreamingServer[usersv1.User]) error {
	return g.usersService.WatchUsers(stream.Context(), req, &grpcServerStreamAdapter{stream})
}

func (g *grpcUsersService) UploadUsers(stream grpc.ClientStreamingServer[usersv1.User, usersv1.UploadUsersResponse]) error {
	resp, err := g.usersService.UploadUsers(stream.Context(), &grpcClientStreamAdapter{stream})
	if err != nil {
		return err
	}
	return stream.SendAndClose(resp)
}

func (g *grpcUsersService) Chat(stream grpc.BidiStreamingServer[usersv1.ChatMessage, usersv1.ChatMessage]) error {
	return g.usersService.Chat(stream.Context(), &grpcBidiStreamAdapter{stream})
}

// grpcServerStreamAdapter adapts grpc.ServerStreamingServer to
// runtime.ServerStream.
type grpcServerStreamAdapter struct {
	stream grpc.ServerStreamingServer[usersv1.User]
}

func (a *grpcServerStreamAdapter) Send(u *usersv1.User) error { return a.stream.Send(u) }

// grpcClientStreamAdapter adapts grpc.ClientStreamingServer to
// runtime.ClientStream.
type grpcClientStreamAdapter struct {
	stream grpc.ClientStreamingServer[usersv1.User, usersv1.UploadUsersResponse]
}

func (a *grpcClientStreamAdapter) Receive() (*usersv1.User, error) { return a.stream.Recv() }

// grpcBidiStreamAdapter adapts grpc.BidiStreamingServer to
// runtime.BidiStream.
type grpcBidiStreamAdapter struct {
	stream grpc.BidiStreamingServer[usersv1.ChatMessage, usersv1.ChatMessage]
}

func (a *grpcBidiStreamAdapter) Send(m *usersv1.ChatMessage) error { return a.stream.Send(m) }
func (a *grpcBidiStreamAdapter) Receive() (*usersv1.ChatMessage, error) {
	return a.stream.Recv()
}

// zaraToGRPC converts a zararpc error to a grpc status error so error codes
// survive the gRPC hop. Genuine gRPC errors (e.g. from stream.Send/Recv) pass
// through unchanged. zararpc codes are numerically identical to grpc codes,
// so the conversion is a direct cast.
func zaraToGRPC(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := grpcstatus.FromError(err); ok {
		return err
	}
	return grpcstatus.Error(grpccodes.Code(status.Code(err)), err.Error())
}

// unaryInterceptor converts zararpc errors to gRPC status errors.
func unaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	resp, err := handler(ctx, req)
	if err != nil {
		return nil, zaraToGRPC(err)
	}
	return resp, nil
}

// streamInterceptor converts zararpc errors to gRPC status errors.
func streamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := handler(srv, ss); err != nil {
		return zaraToGRPC(err)
	}
	return nil
}

func main() {
	svc := &usersService{users: make(map[string]*usersv1.User)}

	// gRPC server with reflection (grpcurl -plaintext localhost:8080 list).
	// Interceptors convert zararpc errors to gRPC status errors.
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(unaryInterceptor),
		grpc.StreamInterceptor(streamInterceptor),
	)
	usersv1.RegisterUsersServiceServer(grpcServer, &grpcUsersService{usersService: svc})
	reflection.Register(grpcServer)

	// HTTP mux: REST + SSE + NDJSON + WebSocket.
	mux := runtime.NewMux()
	if err := usersv1.RegisterUsersServiceRoutes(mux, svc); err != nil {
		log.Fatalf("register service: %v", err)
	}

	// Route by content-type: gRPC requests go to the gRPC server, everything
	// else goes to the HTTP mux. h2c lets both share one cleartext listener.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})

	addr := "localhost:8080"
	log.Printf("combined gRPC + HTTP server listening on http://%s", addr)
	log.Printf("  grpcurl:  grpcurl -plaintext %s list", addr)
	log.Printf("  REST:     curl http://%s/v1/users", addr)
	if err := http.ListenAndServe(addr, h2c.NewHandler(handler, &http2.Server{})); err != nil {
		log.Fatal(err)
	}
}
