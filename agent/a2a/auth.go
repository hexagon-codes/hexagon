package a2a

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ============== 服务端认证 ==============

// AuthValidator 认证验证器接口
// 用于服务端验证客户端请求的认证信息。
type AuthValidator interface {
	// Validate 验证请求
	// 返回用户/客户端标识，如果验证失败返回错误
	Validate(r *http.Request) (string, error)
}

// NoAuthValidator 无认证验证器
// 允许所有请求。
type NoAuthValidator struct{}

// Validate 实现 AuthValidator 接口
func (v *NoAuthValidator) Validate(_ *http.Request) (string, error) {
	return "anonymous", nil
}

// BearerTokenValidator Bearer Token 验证器
type BearerTokenValidator struct {
	// tokens 有效的 token 列表 (token -> client_id)
	tokens map[string]string

	mu sync.RWMutex
}

// NewBearerTokenValidator 创建 Bearer Token 验证器
func NewBearerTokenValidator() *BearerTokenValidator {
	return &BearerTokenValidator{
		tokens: make(map[string]string),
	}
}

// AddToken 添加有效 token
func (v *BearerTokenValidator) AddToken(token, clientID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.tokens[token] = clientID
}

// RemoveToken 移除 token
func (v *BearerTokenValidator) RemoveToken(token string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.tokens, token)
}

// Validate 实现 AuthValidator 接口
func (v *BearerTokenValidator) Validate(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", ErrAuthRequired
	}

	// 解析 Bearer token
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", NewAuthFailedError("invalid authorization header")
	}

	token := parts[1]

	v.mu.RLock()
	clientID, exists := v.tokens[token]
	v.mu.RUnlock()

	if !exists {
		return "", NewAuthFailedError("invalid token")
	}

	return clientID, nil
}

// APIKeyValidator API Key 验证器
type APIKeyValidator struct {
	// name API Key 参数名
	name string

	// in 参数位置 (header, query)
	in string

	// keys 有效的 API Key (key -> client_id)
	keys map[string]string

	mu sync.RWMutex
}

// NewAPIKeyValidator 创建 API Key 验证器
func NewAPIKeyValidator(name, in string) *APIKeyValidator {
	return &APIKeyValidator{
		name: name,
		in:   in,
		keys: make(map[string]string),
	}
}

// AddKey 添加有效 API Key
func (v *APIKeyValidator) AddKey(key, clientID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys[key] = clientID
}

// RemoveKey 移除 API Key
func (v *APIKeyValidator) RemoveKey(key string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.keys, key)
}

// Validate 实现 AuthValidator 接口
func (v *APIKeyValidator) Validate(r *http.Request) (string, error) {
	var key string

	switch v.in {
	case "query":
		key = r.URL.Query().Get(v.name)
	default: // header
		key = r.Header.Get(v.name)
	}

	if key == "" {
		return "", ErrAuthRequired
	}

	v.mu.RLock()
	clientID, exists := v.keys[key]
	v.mu.RUnlock()

	if !exists {
		return "", NewAuthFailedError("invalid API key")
	}

	return clientID, nil
}

// BasicAuthValidator Basic 认证验证器
type BasicAuthValidator struct {
	// credentials 有效的凭证 (username:password -> client_id)
	credentials map[string]string

	mu sync.RWMutex
}

// NewBasicAuthValidator 创建 Basic 认证验证器
func NewBasicAuthValidator() *BasicAuthValidator {
	return &BasicAuthValidator{
		credentials: make(map[string]string),
	}
}

// AddCredentials 添加凭证
func (v *BasicAuthValidator) AddCredentials(username, password, clientID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.credentials[username+":"+password] = clientID
}

// RemoveCredentials 移除凭证
func (v *BasicAuthValidator) RemoveCredentials(username, password string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.credentials, username+":"+password)
}

// Validate 实现 AuthValidator 接口
func (v *BasicAuthValidator) Validate(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", ErrAuthRequired
	}

	// 解析 Basic 认证
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "basic" {
		return "", NewAuthFailedError("invalid authorization header")
	}

	// Base64 解码
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", NewAuthFailedError("invalid base64 encoding")
	}

	cred := string(decoded)

	v.mu.RLock()
	clientID, exists := v.credentials[cred]
	v.mu.RUnlock()

	if !exists {
		return "", NewAuthFailedError("invalid credentials")
	}

	return clientID, nil
}

// ============== 组合验证器 ==============

// ChainValidator 链式验证器
// 按顺序尝试多个验证器，第一个成功的生效。
type ChainValidator struct {
	validators []AuthValidator
}

// NewChainValidator 创建链式验证器
func NewChainValidator(validators ...AuthValidator) *ChainValidator {
	return &ChainValidator{validators: validators}
}

// Validate 实现 AuthValidator 接口
func (v *ChainValidator) Validate(r *http.Request) (string, error) {
	var lastErr error

	for _, validator := range v.validators {
		clientID, err := validator.Validate(r)
		if err == nil {
			return clientID, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return "", lastErr
	}

	return "", ErrAuthRequired
}

// ============== 认证上下文 ==============

// authContextKey 认证上下文键类型
type authContextKey struct{}

// AuthContext 认证上下文
type AuthContext struct {
	// ClientID 客户端 ID
	ClientID string

	// Authenticated 是否已认证
	Authenticated bool

	// AuthTime 认证时间
	AuthTime time.Time

	// Metadata 额外元数据
	Metadata map[string]any
}

// WithAuthContext 将认证上下文添加到 context
func WithAuthContext(ctx context.Context, authCtx *AuthContext) context.Context {
	return context.WithValue(ctx, authContextKey{}, authCtx)
}

// GetAuthContext 从 context 获取认证上下文
func GetAuthContext(ctx context.Context) *AuthContext {
	if authCtx, ok := ctx.Value(authContextKey{}).(*AuthContext); ok {
		return authCtx
	}
	return nil
}

// ============== 认证中间件 ==============

// AuthMiddleware 创建认证中间件
func AuthMiddleware(validator AuthValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 验证请求
			clientID, err := validator.Validate(r)
			if err != nil {
				// 认证失败
				a2aErr := ToA2AError(err)
				w.Header().Set("Content-Type", ContentTypeJSON)
				w.WriteHeader(http.StatusUnauthorized)
				resp := NewJSONRPCErrorResponse(nil, a2aErr)
				writeJSON(w, resp)
				return
			}

			// 创建认证上下文
			authCtx := &AuthContext{
				ClientID:      clientID,
				Authenticated: true,
				AuthTime:      time.Now(),
				Metadata:      make(map[string]any),
			}

			// 添加到 context
			ctx := WithAuthContext(r.Context(), authCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuthMiddleware 可选认证中间件
// 尝试验证但不要求认证。
func OptionalAuthMiddleware(validator AuthValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 尝试验证
			clientID, err := validator.Validate(r)

			var authCtx *AuthContext
			if err == nil {
				authCtx = &AuthContext{
					ClientID:      clientID,
					Authenticated: true,
					AuthTime:      time.Now(),
					Metadata:      make(map[string]any),
				}
			} else {
				authCtx = &AuthContext{
					ClientID:      "anonymous",
					Authenticated: false,
					Metadata:      make(map[string]any),
				}
			}

			ctx := WithAuthContext(r.Context(), authCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeJSON 写入 JSON 响应（内部辅助函数）
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", ContentTypeJSON)
	_ = encodeJSON(w, v)
}

// encodeJSON JSON 编码（内部辅助函数）
func encodeJSON(w http.ResponseWriter, v any) error {
	encoder := newJSONEncoder(w)
	return encoder.Encode(v)
}

// ============== RBAC 支持 ==============

// Permission 权限
type Permission string

const (
	// PermissionRead 读取权限
	PermissionRead Permission = "read"

	// PermissionWrite 写入权限
	PermissionWrite Permission = "write"

	// PermissionAdmin 管理权限
	PermissionAdmin Permission = "admin"

	// PermissionSendMessage 发送消息权限
	PermissionSendMessage Permission = "send_message"

	// PermissionCancelTask 取消任务权限
	PermissionCancelTask Permission = "cancel_task"

	// PermissionConfigPush 配置推送权限
	PermissionConfigPush Permission = "config_push"
)

// RBACValidator RBAC 验证器
type RBACValidator struct {
	// authValidator 底层认证验证器
	authValidator AuthValidator

	// permissions 权限映射 (clientID -> []Permission)
	permissions map[string][]Permission

	mu sync.RWMutex
}

// NewRBACValidator 创建 RBAC 验证器
func NewRBACValidator(authValidator AuthValidator) *RBACValidator {
	return &RBACValidator{
		authValidator: authValidator,
		permissions:   make(map[string][]Permission),
	}
}

// SetPermissions 设置客户端权限
func (v *RBACValidator) SetPermissions(clientID string, permissions ...Permission) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.permissions[clientID] = permissions
}

// AddPermission 添加权限
func (v *RBACValidator) AddPermission(clientID string, permission Permission) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.permissions[clientID] = append(v.permissions[clientID], permission)
}

// HasPermission 检查权限
func (v *RBACValidator) HasPermission(clientID string, permission Permission) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()

	perms := v.permissions[clientID]
	for _, p := range perms {
		if p == permission || p == PermissionAdmin {
			return true
		}
	}

	return false
}

// Validate 实现 AuthValidator 接口
func (v *RBACValidator) Validate(r *http.Request) (string, error) {
	return v.authValidator.Validate(r)
}

// RequirePermission 创建需要特定权限的中间件
func (v *RBACValidator) RequirePermission(permission Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCtx := GetAuthContext(r.Context())
			if authCtx == nil || !authCtx.Authenticated {
				w.Header().Set("Content-Type", ContentTypeJSON)
				w.WriteHeader(http.StatusUnauthorized)
				resp := NewJSONRPCErrorResponse(nil, NewAuthRequiredError())
				writeJSON(w, resp)
				return
			}

			if !v.HasPermission(authCtx.ClientID, permission) {
				w.Header().Set("Content-Type", ContentTypeJSON)
				w.WriteHeader(http.StatusForbidden)
				resp := NewJSONRPCErrorResponse(nil, NewPermissionDeniedError(string(permission)))
				writeJSON(w, resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ============== JSON 编码器 ==============

// newJSONEncoder 创建 JSON 编码器
func newJSONEncoder(w http.ResponseWriter) *json.Encoder {
	return json.NewEncoder(w)
}
