package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHotReloadObserverPanicDoesNotKillWatcher(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("plugins: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewPluginManager()
	watcher := NewHotReloadManager(manager,
		WithHotReloadInterval(10*time.Millisecond),
		WithHotReloadCallback(func(string, *PluginVersion, *PluginVersion, error) {
			panic("hot reload observer failed")
		}),
	)
	if err := watcher.Watch("review-plugin", path); err != nil {
		t.Fatal(err)
	}
	if err := watcher.Start(); err != nil {
		t.Fatal(err)
	}
	defer watcher.Stop()
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for watcher.LastError() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	var panicErr *CallbackPanicError
	if !errors.As(watcher.LastError(), &panicErr) || panicErr.Phase != "hot_reload.observe" {
		t.Fatalf("observer panic not retained: %v", watcher.LastError())
	}
	if !watcher.IsRunning() {
		t.Fatal("watcher stopped after observer panic")
	}
}
