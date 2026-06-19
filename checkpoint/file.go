package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/hash"
)

// File 是基于本地文件系统的 Checkpointer 实现，跨进程重启持久化、并发安全。
//
// 每个命名空间对应目录下的一个 JSON 文件（文件名取命名空间的 sha256 十六进制，
// 避免命名空间含非法路径字符）。写入用"临时文件 + rename"保证原子性。
type File struct {
	dir string
	mu  sync.Mutex // 串行化 read-modify-write，保证并发下链的原子更新
	now func() time.Time
}

// NewFile 创建一个文件 Checkpointer，检查点文件存放于 dir（不存在则创建）。
func NewFile(dir string) (*File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("checkpoint: mkdir %q: %w", dir, err)
	}
	return &File{dir: dir}, nil
}

func (f *File) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// path 返回命名空间对应的检查点文件路径。
func (f *File) path(namespace string) string {
	return filepath.Join(f.dir, hash.SHA256(namespace)+".json")
}

// readChain 读取命名空间的检查点链；文件不存在返回空链。
func (f *File) readChain(namespace string) ([]Checkpoint, error) {
	data, err := os.ReadFile(f.path(namespace))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read %q: %w", namespace, err)
	}
	var chain []Checkpoint
	if err := json.Unmarshal(data, &chain); err != nil {
		return nil, fmt.Errorf("checkpoint: unmarshal %q: %w", namespace, err)
	}
	return chain, nil
}

// writeChain 用"临时文件 + rename"原子写入命名空间的检查点链。
func (f *File) writeChain(namespace string, chain []Checkpoint) error {
	data, err := json.MarshalIndent(chain, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: marshal %q: %w", namespace, err)
	}
	final := f.path(namespace)
	tmp, err := os.CreateTemp(f.dir, ".ckpt-*.tmp")
	if err != nil {
		return fmt.Errorf("checkpoint: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpoint: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpoint: close temp: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("checkpoint: rename: %w", err)
	}
	return nil
}

// Put 持久化一个检查点：同 (Namespace, ID) 覆盖，新 ID 追加到链尾。
func (f *File) Put(_ context.Context, cp Checkpoint) error {
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = f.clock()
	}
	stored := clone(cp)

	f.mu.Lock()
	defer f.mu.Unlock()
	chain, err := f.readChain(cp.Namespace)
	if err != nil {
		return err
	}
	replaced := false
	for i := range chain {
		if chain[i].ID == cp.ID {
			chain[i] = stored
			replaced = true
			break
		}
	}
	if !replaced {
		chain = append(chain, stored)
	}
	return f.writeChain(cp.Namespace, chain)
}

// Get 按 (namespace, id) 精确取检查点。
func (f *File) Get(_ context.Context, namespace, id string) (Checkpoint, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	chain, err := f.readChain(namespace)
	if err != nil {
		return Checkpoint{}, false, err
	}
	for _, cp := range chain {
		if cp.ID == id {
			return cp, true, nil
		}
	}
	return Checkpoint{}, false, nil
}

// Latest 取命名空间内最近写入的检查点。
func (f *File) Latest(_ context.Context, namespace string) (Checkpoint, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	chain, err := f.readChain(namespace)
	if err != nil {
		return Checkpoint{}, false, err
	}
	if len(chain) == 0 {
		return Checkpoint{}, false, nil
	}
	return chain[len(chain)-1], true, nil
}

// List 按写入顺序从新到旧返回命名空间内的检查点；limit<=0 表示不限。
func (f *File) List(_ context.Context, namespace string, limit int) ([]Checkpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	chain, err := f.readChain(namespace)
	if err != nil {
		return nil, err
	}
	out := make([]Checkpoint, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, chain[i])
	}
	return out, nil
}

// Delete 删除整个命名空间的检查点文件。
func (f *File) Delete(_ context.Context, namespace string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.Remove(f.path(namespace)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checkpoint: delete %q: %w", namespace, err)
	}
	return nil
}

// 编译期断言：两个后端都实现 Checkpointer。
var (
	_ Checkpointer = (*Memory)(nil)
	_ Checkpointer = (*File)(nil)
)
