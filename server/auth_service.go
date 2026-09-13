// auth.go: register/login endpoints backed by GORM, plus bcrypt password
// hashing. The service implements the generated usersv1.AuthServiceHandler
// contract (from users.proto), so the same RPCs work over HTTP (generated
// routes with google.api.http annotations) and gRPC (the generated
// RegisterAuthServiceServer converts the gRPC context into runtime.Ctx).
// Bodies are JSON-decoded by the framework (body: "*").
package main

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	usersv1 "github.com/aldok10/zara-rpc-examples/proto/users/v1"
	"github.com/aldok10/zara-rpc/auth/jwt"
	"github.com/aldok10/zara-rpc/codes"
	"github.com/aldok10/zara-rpc/runtime"
	"github.com/aldok10/zara-rpc/status"
)

// HashPassword hashes a plaintext password using bcrypt.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword compares a bcrypt-hashed password with a plaintext candidate.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// authService implements usersv1.AuthServiceHandler. It is public (no auth
// chain) — Register and Login are how clients obtain tokens.
type authService struct {
	usersv1.UnimplementedAuthServiceHandler
	db     *gorm.DB
	issuer *jwt.Issuer
}

// Register creates a user with a bcrypt-hashed password and returns its id.
// The role defaults to "user"; only the demo server may mint admins.
func (s *authService) Register(ctx runtime.Ctx, req *usersv1.RegisterRequest) (*usersv1.RegisterResponse, error) {
	username, password, email, role := req.Username, req.Password, req.Email, req.Role
	if username == "" || password == "" {
		return nil, status.NewErrorf(codes.CodeInvalidArgument, "username and password required")
	}

	// Check uniqueness.
	var existing usersv1.UserRow
	if err := s.db.Where("name = ?", username).First(&existing).Error; err == nil {
		return nil, status.NewErrorf(codes.CodeAlreadyExists, "username %q already taken", username)
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "hash password: %v", err)
	}
	if role == "" {
		role = "user"
	}
	row := &usersv1.UserRow{
		Id:           uuid.NewString(),
		Name:         username,
		Email:        email,
		PasswordHash: hash,
		Role:         role,
	}
	if err := s.db.Create(row).Error; err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "create user: %v", err)
	}
	return &usersv1.RegisterResponse{Id: row.Id, Username: row.Name}, nil
}

// Login verifies the password and returns a signed JWT carrying the user's
// role claim.
func (s *authService) Login(ctx runtime.Ctx, req *usersv1.LoginRequest) (*usersv1.LoginResponse, error) {
	username, password := req.Username, req.Password
	if username == "" || password == "" {
		return nil, status.NewErrorf(codes.CodeInvalidArgument, "username and password required")
	}

	var row usersv1.UserRow
	if err := s.db.Where("name = ?", username).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.NewErrorf(codes.CodeNotFound, "user %q not found", username)
		}
		return nil, status.NewErrorf(codes.CodeInternal, "login: %v", err)
	}
	if !CheckPassword(row.PasswordHash, password) {
		return nil, status.NewErrorf(codes.CodeUnauthenticated, "invalid password")
	}
	token, err := s.issuer.Issue(row.Id, map[string]any{"role": row.Role}, time.Hour)
	if err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "issue token: %v", err)
	}
	return &usersv1.LoginResponse{
		Token: token,
		User:  &usersv1.User{Id: row.Id, Name: row.Name, Email: row.Email},
	}, nil
}