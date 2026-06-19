package credential

import (
	"errors"
	"testing"

	"github.com/hexagon-codes/toolkit/crypto/aes"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key, err := aes.GenerateKey(32)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestCipher_RoundTrip 加密后能正确解密回明文。
func TestCipher_RoundTrip(t *testing.T) {
	c := newTestCipher(t)
	secret := "sk-proj-abcdef123456" //gitleaks:allow 测试假凭据，非真实密钥

	ct, err := c.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	if ct == secret {
		t.Error("密文不应等于明文")
	}
	got, err := c.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Errorf("解密 = %q, want %q", got, secret)
	}
}

// TestCipher_RandomNonce 同一明文两次加密密文不同（随机 nonce）。
func TestCipher_RandomNonce(t *testing.T) {
	c := newTestCipher(t)
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if a == b {
		t.Error("同一明文两次加密应得到不同密文（随机 nonce）")
	}
}

// TestCipher_WrongKeyFails 用不同密钥解密应失败（GCM 认证）。
func TestCipher_WrongKeyFails(t *testing.T) {
	c1 := newTestCipher(t)
	c2 := newTestCipher(t)

	ct, _ := c1.Encrypt("secret")
	if _, err := c2.Decrypt(ct); err == nil {
		t.Error("错误密钥解密应失败")
	}
}

// TestNewCipher_InvalidKeySize 非法密钥长度被拒绝。
func TestNewCipher_InvalidKeySize(t *testing.T) {
	if _, err := NewCipher([]byte("short")); err == nil {
		t.Error("非法密钥长度应返回错误")
	}
}

// TestStore_SetGetDelete 验证加密存储的增删查。
func TestStore_SetGetDelete(t *testing.T) {
	s := NewStore(newTestCipher(t))

	if err := s.Set("openai", "sk-123456789"); err != nil {
		t.Fatal(err)
	}
	if !s.Has("openai") {
		t.Error("Set 后应 Has")
	}
	got, err := s.Get("openai")
	if err != nil || got != "sk-123456789" {
		t.Errorf("Get = (%q,%v), want (sk-123456789,nil)", got, err)
	}

	// 内存中保存的是密文，不是明文
	if s.secrets["openai"] == "sk-123456789" {
		t.Error("Store 内部应保存密文而非明文")
	}

	s.Delete("openai")
	if _, err := s.Get("openai"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除后 Get 应返回 ErrNotFound, got %v", err)
	}
}

// TestStore_GetMissing 不存在的凭据返回 ErrNotFound。
func TestStore_GetMissing(t *testing.T) {
	s := NewStore(newTestCipher(t))
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("应返回 ErrNotFound, got %v", err)
	}
}

// TestMask 脱敏保留前 2 后 4，过短全脱敏。
func TestMask(t *testing.T) {
	cases := map[string]string{
		"sk-proj-abcdef1234": "sk************1234",
		"short":              "*****",
		"123456":             "******",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}
