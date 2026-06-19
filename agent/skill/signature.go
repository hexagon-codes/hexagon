// Package skill 的签名验证模块
//
// 提供技能签名验证能力，确保技能来源可信：
//   - HMAC-SHA256 签名
//   - 通用 Verifier 接口（可扩展 Ed25519 等）
//
// 使用示例：
//
//	signer := skill.NewHMACSigner([]byte("secret"))
//	sig := signer.Sign([]byte("data"))
//	err := signer.Verify([]byte("data"), sig)
package skill

import (
	"errors"
	"fmt"

	"github.com/hexagon-codes/toolkit/crypto/sign"
)

// ============== 错误定义 ==============

var (
	// ErrInvalidSignature 签名验证失败
	ErrInvalidSignature = errors.New("skill: 签名验证失败")

	// ErrNoVerifier 未配置签名验证器
	ErrNoVerifier = errors.New("skill: 未配置签名验证器")
)

// ============== 验证器接口 ==============

// Verifier 签名验证器接口
type Verifier interface {
	// Verify 验证数据签名
	Verify(data []byte, signature []byte) error

	// Algorithm 返回签名算法名称
	Algorithm() string
}

// Signer 签名器接口（同时具备签名和验证能力）
type Signer interface {
	Verifier

	// Sign 对数据签名
	Sign(data []byte) []byte
}

// ============== HMAC-SHA256 ==============

// HMACSigner 基于 HMAC-SHA256 的签名器
type HMACSigner struct {
	secret []byte
}

// NewHMACSigner 创建 HMAC-SHA256 签名器
//
// secret 会被拷贝，调用方后续修改不影响签名器。
func NewHMACSigner(secret []byte) *HMACSigner {
	s := make([]byte, len(secret))
	copy(s, secret)
	return &HMACSigner{secret: s}
}

// Sign 计算 HMAC-SHA256 签名
func (h *HMACSigner) Sign(data []byte) []byte {
	return sign.HMACSHA256(data, h.secret)
}

// Verify 验证 HMAC-SHA256 签名
func (h *HMACSigner) Verify(data []byte, signature []byte) error {
	if !sign.VerifyHMACSHA256(data, h.secret, signature) {
		return ErrInvalidSignature
	}
	return nil
}

// Algorithm 返回算法名称
func (h *HMACSigner) Algorithm() string {
	return "hmac-sha256"
}

// ============== 技能签名验证 ==============

// SkillSignature 技能签名信息
type SkillSignature struct {
	// SkillName 技能名称
	SkillName string

	// Version 技能版本
	Version string

	// Signature 签名数据
	Signature []byte
}

// SignSkill 对技能进行签名
//
// 将技能的 Name+Version 序列化后计算签名。
func SignSkill(s *Skill, signer Signer) *SkillSignature {
	data := skillSignData(s.Name, s.Version)
	return &SkillSignature{
		SkillName: s.Name,
		Version:   s.Version,
		Signature: signer.Sign(data),
	}
}

// VerifySkill 验证技能签名
func VerifySkill(s *Skill, sig *SkillSignature, verifier Verifier) error {
	if verifier == nil {
		return ErrNoVerifier
	}
	if s.Name != sig.SkillName || s.Version != sig.Version {
		return fmt.Errorf("%w: 技能名称或版本不匹配", ErrInvalidSignature)
	}
	data := skillSignData(s.Name, s.Version)
	return verifier.Verify(data, sig.Signature)
}

// skillSignData 构造签名数据
func skillSignData(name, version string) []byte {
	return []byte(fmt.Sprintf("skill:%s:v:%s", name, version))
}
