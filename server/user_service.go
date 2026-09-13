// user_service.go: UsersService backed by SQLite via GORM. The same struct
// implements the generated usersv1.UsersServiceHandler contract, so it
// serves both HTTP routes and gRPC (the generated RegisterUsersServiceServer
// converts the gRPC context into runtime.Ctx).
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	usersv1 "github.com/aldok10/zara-rpc-examples/proto/users/v1"
	"github.com/aldok10/zara-rpc/codes"
	"github.com/aldok10/zara-rpc/runtime"
	"github.com/aldok10/zara-rpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// usersService implements usersv1.UsersServiceHandler (the zararpc HTTP
// contract) with persistent storage in the users table.
type usersService struct {
	usersv1.UnimplementedUsersServiceHandler
	db *gorm.DB
}

// rowToUser converts a GORM UserRow (the database model) to the wire User
// type. The password_hash and role columns never leave the server.
func rowToUser(row *usersv1.UserRow) *usersv1.User {
	return &usersv1.User{Id: row.Id, Name: row.Name, Email: row.Email}
}

func (s *usersService) GetUser(ctx runtime.Ctx, req *usersv1.GetUserRequest) (*usersv1.User, error) {
	// Demo: handlers can branch on the negotiated transport.
	if ctx.IsJsonCodec() {
		log.Printf("GetUser: JSON over %s", ctx.Protocol())
	} else if ctx.IsGRPC() {
		log.Printf("GetUser: gRPC (protobuf)")
	}
	// Generic payload access: the same message the handler received.
	if typed := ctx.Request[usersv1.GetUserRequest](); typed != nil && typed.Id != req.Id {
		return nil, status.NewErrorf(codes.CodeInternal, "ctx payload mismatch")
	}
	var row usersv1.UserRow
	err := s.db.Where("id = ?", req.Id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.NewErrorf(codes.CodeNotFound, "user %q not found", req.Id)
	}
	if err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "get user: %v", err)
	}
	return rowToUser(&row), nil
}

// GetUserByID is the automatic query-binding demo: the request has no path
// params and no body, so per google.api.http rules the id field is bound
// from the query string (?id=...). The handler is identical to GetUser.
func (s *usersService) GetUserByID(ctx runtime.Ctx, req *usersv1.GetUserRequest) (*usersv1.User, error) {
	var row usersv1.UserRow
	err := s.db.Where("id = ?", req.Id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.NewErrorf(codes.CodeNotFound, "user %q not found", req.Id)
	}
	if err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "get user by id: %v", err)
	}
	return rowToUser(&row), nil
}

func (s *usersService) ListUsers(ctx runtime.Ctx, req *usersv1.ListUsersRequest) (*usersv1.ListUsersResponse, error) {
	var rows []usersv1.UserRow
	if err := s.db.Order("name").Find(&rows).Error; err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "list users: %v", err)
	}
	resp := &usersv1.ListUsersResponse{}
	for i := range rows {
		resp.Users = append(resp.Users, rowToUser(&rows[i]))
	}
	return resp, nil
}

func (s *usersService) CreateUser(ctx runtime.Ctx, req *usersv1.CreateUserRequest) (*usersv1.User, error) {
	// Demo: read the raw body and a cookie from the transport payload.
	body := ctx.Body()
	if c, err := ctx.Cookie("session"); err == nil {
		log.Printf("CreateUser: session cookie=%q body=%d bytes", c.Value, len(body))
	}
	row := &usersv1.UserRow{
		Id:    uuid.NewString(),
		Name:  req.Name,
		Email: req.Email,
		Role:  "user",
	}
	if err := s.db.Create(row).Error; err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "create user: %v", err)
	}
	return rowToUser(row), nil
}

func (s *usersService) UpdateUser(ctx runtime.Ctx, req *usersv1.UpdateUserRequest) (*usersv1.User, error) {
	var row usersv1.UserRow
	err := s.db.Where("id = ?", req.Id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.NewErrorf(codes.CodeNotFound, "user %q not found", req.Id)
	}
	if err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "get user: %v", err)
	}
	if req.Name != "" {
		row.Name = req.Name
	}
	if req.Email != "" {
		row.Email = req.Email
	}
	if err := s.db.Save(&row).Error; err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "update user: %v", err)
	}
	return rowToUser(&row), nil
}

func (s *usersService) DeleteUser(ctx runtime.Ctx, req *usersv1.DeleteUserRequest) (*emptypb.Empty, error) {
	res := s.db.Where("id = ?", req.Id).Delete(&usersv1.UserRow{})
	if res.Error != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "delete user: %v", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, status.NewErrorf(codes.CodeNotFound, "user %q not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}

func (s *usersService) Echo(ctx runtime.Ctx, req *usersv1.EchoRequest) (*usersv1.EchoResponse, error) {
	return &usersv1.EchoResponse{Message: req.Message}, nil
}

// GetUserProfile returns the stored user; the response_body annotation on
// the route makes the HTTP body just the name field, not the full message.
func (s *usersService) GetUserProfile(ctx runtime.Ctx, req *usersv1.GetUserRequest) (*usersv1.User, error) {
	var row usersv1.UserRow
	err := s.db.Where("id = ?", req.Id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.NewErrorf(codes.CodeNotFound, "user %q not found", req.Id)
	}
	if err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "get user: %v", err)
	}
	return rowToUser(&row), nil
}

// ActivateUser is reached via the custom verb route POST /v1/users/{name}:activate.
func (s *usersService) ActivateUser(ctx runtime.Ctx, req *usersv1.ActivateUserRequest) (*usersv1.User, error) {
	var row usersv1.UserRow
	err := s.db.Where("name = ?", req.Name).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.NewErrorf(codes.CodeNotFound, "user %q not found", req.Name)
	}
	if err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "get user: %v", err)
	}
	return rowToUser(&row), nil
}

// WatchUsers streams all users every interval_seconds (SSE transport).
func (s *usersService) WatchUsers(ctx runtime.Ctx, req *usersv1.WatchUsersRequest, stream runtime.ServerStream[*usersv1.User]) error {
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
			var rows []usersv1.UserRow
			if err := s.db.Find(&rows).Error; err != nil {
				return status.NewErrorf(codes.CodeInternal, "watch users: %v", err)
			}
			for i := range rows {
				if err := stream.Send(rowToUser(&rows[i])); err != nil {
					return err
				}
			}
		}
	}
}

// UploadUsers reads a stream of users and stores them (NDJSON transport).
func (s *usersService) UploadUsers(ctx runtime.Ctx, stream runtime.ClientStream[*usersv1.User]) (*usersv1.UploadUsersResponse, error) {
	var count int32
	for {
		u, err := stream.Receive()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		row := &usersv1.UserRow{
			Id:    uuid.NewString(),
			Name:  u.Name,
			Email: u.Email,
			Role:  "user",
		}
		if err := s.db.Create(row).Error; err != nil {
			return nil, status.NewErrorf(codes.CodeInternal, "upload user: %v", err)
		}
		count++
	}
	return &usersv1.UploadUsersResponse{Count: count}, nil
}

// Chat echoes messages back (WebSocket transport).
func (s *usersService) Chat(ctx runtime.Ctx, stream runtime.BidiStream[*usersv1.ChatMessage, *usersv1.ChatMessage]) error {
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

var _ = fmt.Sprintf // keep fmt import if unused after edits
