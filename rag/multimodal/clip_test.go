package multimodal

import (
	"context"
	"errors"
	"testing"
)

// fakeCLIP 是测试用的 CLIP 后端。
type fakeCLIP struct{}

func (fakeCLIP) EmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2}
	}
	return out, nil
}

func (fakeCLIP) EmbedImage(ctx context.Context, image *Content) ([]float32, error) {
	return []float32{0.3, 0.4}, nil
}

// TestCLIP_NoBackend 未注入后端时返回 ErrCLIPNotImplemented（安全默认，不伪造向量）。
func TestCLIP_NoBackend(t *testing.T) {
	e := NewCLIPEmbedder("", "")
	if e.IsImplemented() {
		t.Error("无后端时 IsImplemented 应为 false")
	}
	if _, err := e.EmbedText(context.Background(), []string{"x"}); !errors.Is(err, ErrCLIPNotImplemented) {
		t.Errorf("无后端 EmbedText 应返回 ErrCLIPNotImplemented, got %v", err)
	}
	if _, err := e.EmbedImage(context.Background(), &Content{}); !errors.Is(err, ErrCLIPNotImplemented) {
		t.Errorf("无后端 EmbedImage 应返回 ErrCLIPNotImplemented, got %v", err)
	}
}

// TestCLIP_WithBackend 注入后端后方法委托给后端。
func TestCLIP_WithBackend(t *testing.T) {
	e := NewCLIPEmbedder("http://clip", "key", WithCLIPBackend(fakeCLIP{}))
	if !e.IsImplemented() {
		t.Fatal("注入后端后 IsImplemented 应为 true")
	}

	vecs, err := e.EmbedText(context.Background(), []string{"a", "b"})
	if err != nil || len(vecs) != 2 {
		t.Fatalf("EmbedText = (%v,%v), want 2 向量", vecs, err)
	}
	img, err := e.EmbedImage(context.Background(), &Content{})
	if err != nil || len(img) != 2 {
		t.Fatalf("EmbedImage = (%v,%v)", img, err)
	}
	imgs, err := e.EmbedImages(context.Background(), []*Content{{}, {}})
	if err != nil || len(imgs) != 2 {
		t.Fatalf("EmbedImages = (%v,%v), want 2", imgs, err)
	}
}
