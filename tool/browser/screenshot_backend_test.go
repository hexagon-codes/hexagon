package browser

import (
	"context"
	"encoding/base64"
	"testing"
)

// fakeShot 是测试用的截图后端，返回固定字节。
type fakeShot struct{}

func (fakeShot) Capture(ctx context.Context, url string, opts ScreenshotOptions) ([]byte, string, error) {
	return []byte("PNGBYTES"), "png", nil
}

// TestScreenshot_NoBackend 未注入后端时返回带 note 标注的占位 SVG（不伪造真实截图）。
func TestScreenshot_NoBackend(t *testing.T) {
	st := NewScreenshotTool()
	res, err := st.Execute(context.Background(), map[string]any{"url": "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("结果类型应为 map, got %T", res)
	}
	if m["format"] != "svg" {
		t.Errorf("无后端应返回 svg 占位, got %v", m["format"])
	}
	if _, hasNote := m["note"]; !hasNote {
		t.Error("占位结果应带 note 说明需要浏览器后端")
	}
}

// TestScreenshot_WithBackend 注入后端后返回真实截图字节，且不带占位 note。
func TestScreenshot_WithBackend(t *testing.T) {
	st := NewScreenshotTool().WithBackend(fakeShot{})
	res, err := st.Execute(context.Background(), map[string]any{"url": "http://x", "full_page": true})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["format"] != "png" {
		t.Errorf("注入后端应返回真实格式 png, got %v", m["format"])
	}
	decoded, err := base64.StdEncoding.DecodeString(m["image"].(string))
	if err != nil || string(decoded) != "PNGBYTES" {
		t.Errorf("应返回后端真实字节, got %q (err %v)", decoded, err)
	}
	if _, hasNote := m["note"]; hasNote {
		t.Error("真实截图结果不应带占位 note")
	}
}
