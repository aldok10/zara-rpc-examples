// todo_service.go: TodoService backed by SQLite via GORM. The generated
// Todo struct IS the GORM model — the (zara.options.tags) on every field
// drive the schema with no hand-written mapping struct. Every handler
// reads the authenticated claims and scopes by owner_id.
package main

import (
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	todosv1 "github.com/aldok10/zara-rpc-examples/proto/todos/v1"
	"github.com/aldok10/zara-rpc/auth/jwt"
	"github.com/aldok10/zara-rpc/codes"
	"github.com/aldok10/zara-rpc/runtime"
	"github.com/aldok10/zara-rpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type todoService struct {
	todosv1.UnimplementedTodoServiceHandler
	db *gorm.DB
}

// claimsSubject returns the authenticated user's subject, or an
// unauthenticated error when the JWT interceptor did not attach claims.
func claimsSubject(ctx runtime.Ctx) (string, error) {
	claims, ok := jwt.ClaimsFromContext(ctx)
	if !ok {
		return "", status.NewErrorf(codes.CodeUnauthenticated, "missing claims")
	}
	return claims.Subject, nil
}

func (s *todoService) CreateTodo(ctx runtime.Ctx, req *todosv1.CreateTodoRequest) (*todosv1.Todo, error) {
	owner, err := claimsSubject(ctx)
	if err != nil {
		return nil, err
	}
	todo := &todosv1.Todo{
		Id:      uuid.NewString(),
		OwnerId: owner,
		Title:   req.Title,
	}
	if err := s.db.Create(todo).Error; err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "create todo: %v", err)
	}
	return todo, nil
}

func (s *todoService) ListTodos(ctx runtime.Ctx, req *todosv1.ListTodosRequest) (*todosv1.ListTodosResponse, error) {
	owner, err := claimsSubject(ctx)
	if err != nil {
		return nil, err
	}
	var todos []*todosv1.Todo
	if err := s.db.Where("owner_id = ?", owner).Find(&todos).Error; err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "list todos: %v", err)
	}
	return &todosv1.ListTodosResponse{Todos: todos}, nil
}

func (s *todoService) GetTodo(ctx runtime.Ctx, req *todosv1.GetTodoRequest) (*todosv1.Todo, error) {
	owner, err := claimsSubject(ctx)
	if err != nil {
		return nil, err
	}
	var todo todosv1.Todo
	err = s.db.Where("id = ? AND owner_id = ?", req.Id, owner).First(&todo).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.NewErrorf(codes.CodeNotFound, "todo %q not found", req.Id)
	}
	if err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "get todo: %v", err)
	}
	return &todo, nil
}

func (s *todoService) UpdateTodo(ctx runtime.Ctx, req *todosv1.UpdateTodoRequest) (*todosv1.Todo, error) {
	owner, err := claimsSubject(ctx)
	if err != nil {
		return nil, err
	}
	var todo todosv1.Todo
	err = s.db.Where("id = ? AND owner_id = ?", req.Id, owner).First(&todo).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.NewErrorf(codes.CodeNotFound, "todo %q not found", req.Id)
	}
	if err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "get todo: %v", err)
	}
	if req.Title != "" {
		todo.Title = req.Title
	}
	todo.Done = req.Done
	if err := s.db.Save(&todo).Error; err != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "update todo: %v", err)
	}
	return &todo, nil
}

func (s *todoService) DeleteTodo(ctx runtime.Ctx, req *todosv1.DeleteTodoRequest) (*emptypb.Empty, error) {
	owner, err := claimsSubject(ctx)
	if err != nil {
		return nil, err
	}
	res := s.db.Where("id = ? AND owner_id = ?", req.Id, owner).Delete(&todosv1.Todo{})
	if res.Error != nil {
		return nil, status.NewErrorf(codes.CodeInternal, "delete todo: %v", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, status.NewErrorf(codes.CodeNotFound, "todo %q not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}
