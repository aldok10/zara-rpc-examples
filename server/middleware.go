// middleware.go: gRPC bridge helpers, auth chain, and error conversion.
// The generated .grpc.go files (RegisterXxxServiceServer) convert incoming
// gRPC metadata into runtime.Ctx via the package-level grpcCtxFor, so the
// same handlers serve both HTTP and gRPC. This file holds the auth chain
// and the gRPC-side auth interceptors that run it before the generated
// handlers.
package main

import (
	"strings"

	"github.com/aldok10/zara-rpc/auth/interceptor"
	"github.com/aldok10/zara-rpc/auth/jwt"
	"github.com/aldok10/zara-rpc/auth/rbac"
	"github.com/aldok10/zara-rpc/middleware"
	"github.com/aldok10/zara-rpc/runtime"
)

// authChains builds the shared authentication + authorization interceptor
// chains. The unary chain (public filter → JWT → RBAC) guards everything:
// HTTP unary calls, gRPC unary and stream establishment (via grpcbridge),
// and HTTP stream establishment (Handler.streamAuth runs the same unary
// chain before the first stream byte). The secret is the same random key
// the issuer uses, so tokens minted by Login validate here.
//
// The stream chain wraps established HTTP streams: PublicStream marks
// public streams, JWTStream re-attaches claims so streaming handlers can
// read them via stream.Context(). RBACStream is deliberately NOT in this
// list: authorization already happened at connection level with claims
// present, and stream interceptors evaluate eagerly at wrap time from the
// innermost outward — an RBACStream placed after JWTStream in the list
// would check the raw context BEFORE JWTStream attaches its claims and
// deny every stream. Order-dependent policy checks belong in the unary
// chain, which runs lazily at call time with a properly built context.
func authChains(secret []byte) ([]middleware.UnaryInterceptor, []middleware.StreamInterceptor, error) {
	validator, err := jwt.NewJWTValidator(secret)
	if err != nil {
		return nil, nil, err
	}
	authorizer, err := rbac.NewStatic(demoPolicy)
	if err != nil {
		return nil, nil, err
	}
	return []middleware.UnaryInterceptor{
		interceptor.Public(isPublic),
		interceptor.JWT(validator),
		interceptor.RBAC(authorizer),
	}, []middleware.StreamInterceptor{
		interceptor.PublicStream(isPublic),
		interceptor.JWTStream(validator),
		interceptor.RBACStream(authorizer),
	}, nil
}

// demoPolicy is the demo RBAC policy:
//
//   - admins (role=admin) may call anything;
//   - any authenticated caller may read (Get* prefix), list users, and Echo;
//   - readers (role=user) are denied DeleteUser;
//   - user and admin roles may call TodoService (the todos demo).
const demoPolicy = `{
  "name": "demo-policy",
  "allow_rules": [
    {"name": "admins", "principals": [{"authenticated": {"claim": "role", "value": "admin"}}], "permissions": [{"any": true}]},
    {"name": "readers", "principals": [{"any": true}], "permissions": [
      {"requested_path": {"prefix": "/acme.users.v1.UsersService/Get"}},
      {"requested_path": {"exact": "/acme.users.v1.UsersService/ListUsers"}},
      {"requested_path": {"exact": "/acme.users.v1.UsersService/Echo"}}
    ]},
    {"name": "todo-users", "principals": [{"authenticated": {"claim": "role", "value": "user"}}], "permissions": [
      {"requested_path": {"prefix": "/acme.todos.v1.TodoService/"}}
    ]}
  ],
  "deny_rules": [
    {"name": "no-delete-for-readers", "principals": [{"authenticated": {"claim": "role", "value": "user"}}], "permissions": [{"requested_path": {"exact": "/acme.users.v1.UsersService/DeleteUser"}}]}
  ]
}`

// isPublic reports whether a request bypasses the auth chain, reading the
// request from the interceptor context. The auth service itself is public —
// clients call Register/Login to obtain tokens, so those RPCs cannot require
// one. gRPC server reflection is also public so grpcurl/buf can discover
// services without a token. The same predicate serves HTTP (ctx.Spec().Path)
// and gRPC (ctx.Spec().Procedure), so one chain protects both transports.
func isPublic(ctx runtime.Ctx) bool {
	if p := ctx.Spec().Procedure; p != "" {
		return strings.HasPrefix(p, "/acme.users.v1.AuthService/") ||
			strings.HasPrefix(p, "/grpc.reflection.v1.") ||
			strings.HasPrefix(p, "/grpc.reflection.v1alpha.")
	}
	return strings.HasPrefix(ctx.Spec().Path, "/v1/auth/")
}
