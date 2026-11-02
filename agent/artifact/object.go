package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var ErrObjectNotFound = errors.New("artifact object not found")
var ErrObjectDigest = errors.New("artifact object digest mismatch")

// ObjectStore 的 key 是稳定对象身份，Put 必须以同 key/摘要幂等且拒绝不同内容覆盖。
// 对象存储连接与关闭责任归宿主；Get 返回实际字节，不使用过期下载 URL 代替稳定寻址。
type ObjectStore interface {
	Put(context.Context, string, []byte, string) error
	Get(context.Context, string) ([]byte, error)
}

// PendingRegistrationError 保留补登记需要的原对象描述，调用者派发前也必须保存该身份。
// 不能把保存此错误或另写修复记录当作已存在对象身份的唯一来源。
type PendingRegistrationError struct {
	Artifact Artifact
	Cause    error
}

func (e *PendingRegistrationError) Error() string {
	return fmt.Sprintf("artifact registration pending: %v", e.Cause)
}
func (e *PendingRegistrationError) Unwrap() error { return e.Cause }

func contentDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// SaveObject 使用预先分配的 Artifact/对象身份保存字节后登记。
// 若原对象已存在，仅核对内容；重复调用不重新生成或覆盖原字节。
func (s *Service) SaveObject(ctx context.Context, objects ObjectStore, art Artifact) (string, error) {
	if objects == nil || art.ID == "" || art.ObjectKey == "" {
		return "", fmt.Errorf("artifact: stable artifact ID and object key are required")
	}
	digest := contentDigest(art.Data)
	if art.Digest != "" && art.Digest != digest {
		return "", ErrObjectDigest
	}
	art.Digest, art.Size = digest, int64(len(art.Data))
	existing, err := objects.Get(ctx, art.ObjectKey)
	if errors.Is(err, ErrObjectNotFound) {
		if err := objects.Put(ctx, art.ObjectKey, art.Data, art.Digest); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if contentDigest(existing) != art.Digest {
		return "", ErrObjectDigest
	}
	art.Data = nil
	return s.RegisterObject(ctx, objects, art)
}

// RegisterObject 只查回并登记同一对象，不调用生成器或 Put；适用于字节成功、登记失败后的重启接续。
func (s *Service) RegisterObject(ctx context.Context, objects ObjectStore, art Artifact) (string, error) {
	if objects == nil || art.ID == "" || art.ObjectKey == "" || art.Digest == "" {
		return "", fmt.Errorf("artifact: stable object reference and digest are required")
	}
	data, err := objects.Get(ctx, art.ObjectKey)
	if err != nil {
		return "", err
	}
	if contentDigest(data) != art.Digest {
		return "", ErrObjectDigest
	}
	art.Data, art.Size, art.RegistrationStatus = nil, int64(len(data)), "registered"
	if art.CreatedAt.IsZero() {
		art.CreatedAt = time.Now().UTC()
	}
	creator, ok := s.store.(versionedCreator)
	if !ok {
		return "", &PendingRegistrationError{Artifact: art, Cause: ErrAtomicCreateUnsupported}
	}
	id, err := creator.CreateNextVersion(ctx, art)
	if err == nil {
		return id, nil
	}
	if errors.Is(err, ErrArtifactConflict) {
		old, lookupErr := s.store.Get(ctx, art.ID)
		if lookupErr == nil && old.ObjectKey == art.ObjectKey && old.Digest == art.Digest && old.OperationID == art.OperationID && old.SessionID == art.SessionID && old.Name == art.Name {
			return old.ID, nil
		}
		if lookupErr != nil {
			err = lookupErr
		}
	}
	return "", &PendingRegistrationError{Artifact: art, Cause: err}
}
