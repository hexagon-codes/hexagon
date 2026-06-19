// Package credential 提供凭据（API Key、密钥等敏感串）的对称加密与加密存储能力。
//
// 底层复用 toolkit/crypto/aes 的 AES-GCM 认证加密（随机 nonce + 完整性校验），
// 在其上提供两层易用封装：
//   - Cipher：单串加解密；
//   - Store：命名凭据的"加密落库 / 取用解密"存储，敏感值在内存中始终以密文保存。
//
// 设计遵循 CLAUDE.md 基础设施复用原则：加密算法用 toolkit/crypto/aes，本包只做
// 面向凭据场景的语义封装（密钥校验、命名存储、脱敏展示）。
package credential

import (
	"fmt"
	"strings"
	"sync"

	"github.com/hexagon-codes/toolkit/crypto/aes"
)

// ErrNotFound 表示请求的命名凭据不存在。
var ErrNotFound = fmt.Errorf("credential: 凭据不存在")

// Cipher 用 AES-GCM 对凭据做对称加解密。
//
// GCM 为认证加密：密文被篡改或密钥不符时解密会失败，而非返回错误明文。
type Cipher struct {
	key string
}

// NewCipher 用给定密钥创建 Cipher。
//
// key 长度必须为 16/24/32 字节，对应 AES-128/192/256。
// 可用 toolkit/crypto/aes.GenerateKey(32) 生成安全随机密钥。
func NewCipher(key []byte) (*Cipher, error) {
	switch len(key) {
	case 16, 24, 32:
	default:
		return nil, fmt.Errorf("credential: AES 密钥长度必须为 16/24/32 字节, 实际 %d", len(key))
	}
	return &Cipher{key: string(key)}, nil
}

// Encrypt 加密明文凭据，返回 base64 密文。
//
// 含随机 nonce，故同一明文每次加密的密文不同（防止密文比对泄露）。
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	return aes.EncryptGCMString(plaintext, c.key)
}

// Decrypt 解密 base64 密文，返回明文凭据。
//
// 密文被篡改或密钥不符时返回错误（GCM 认证失败）。
func (c *Cipher) Decrypt(ciphertext string) (string, error) {
	return aes.DecryptGCMString(ciphertext, c.key)
}

// Store 是加密的命名凭据存储：值在内存中以密文保存，Get 时按需解密。并发安全。
type Store struct {
	cipher  *Cipher
	mu      sync.RWMutex
	secrets map[string]string // name -> base64 密文
}

// NewStore 创建凭据存储。
func NewStore(cipher *Cipher) *Store {
	return &Store{cipher: cipher, secrets: make(map[string]string)}
}

// Set 加密并存储一条命名凭据（覆盖同名旧值）。
func (s *Store) Set(name, plaintext string) error {
	ct, err := s.cipher.Encrypt(plaintext)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.secrets[name] = ct
	s.mu.Unlock()
	return nil
}

// Get 解密并返回命名凭据；不存在时返回 ErrNotFound。
func (s *Store) Get(name string) (string, error) {
	s.mu.RLock()
	ct, ok := s.secrets[name]
	s.mu.RUnlock()
	if !ok {
		return "", ErrNotFound
	}
	return s.cipher.Decrypt(ct)
}

// Has 判断是否存在某条凭据。
func (s *Store) Has(name string) bool {
	s.mu.RLock()
	_, ok := s.secrets[name]
	s.mu.RUnlock()
	return ok
}

// Delete 删除一条凭据（不存在时无操作）。
func (s *Store) Delete(name string) {
	s.mu.Lock()
	delete(s.secrets, name)
	s.mu.Unlock()
}

// Names 返回所有凭据名（顺序不定）。
func (s *Store) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.secrets))
	for n := range s.secrets {
		names = append(names, n)
	}
	return names
}

// Mask 对敏感串脱敏，便于日志/展示：保留前 2、后 4 位，其余以 * 替代；
// 长度 <= 6 的串全部以 * 替代。
func Mask(secret string) string {
	if len(secret) <= 6 {
		return strings.Repeat("*", len(secret))
	}
	return secret[:2] + strings.Repeat("*", len(secret)-6) + secret[len(secret)-4:]
}
