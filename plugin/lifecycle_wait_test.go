package plugin

import (
	"context"
	"errors"
	"testing"
	"time"
)

type waitingStartPlugin struct {
	*BasePlugin
	entered, release chan struct{}
}

func (p *waitingStartPlugin) Start(ctx context.Context) error {
	close(p.entered)
	<-p.release
	return p.BasePlugin.Start(ctx)
}
func TestLifecycleOperationWaitIsCancelable(t *testing.T) {
	for _, operation := range []string{"init", "start", "stop"} {
		t.Run(operation, func(t *testing.T) {
			m := NewPluginManager()
			p := &waitingStartPlugin{NewBasePlugin(PluginInfo{Name: "owned", Version: "1"}), make(chan struct{}), make(chan struct{})}
			if e := m.Load(p); e != nil {
				t.Fatal(e)
			}
			if e := m.Lifecycle.Init(t.Context(), "owned", nil); e != nil {
				t.Fatal(e)
			}
			first := make(chan error, 1)
			go func() { first <- m.Lifecycle.Start(t.Context(), "owned") }()
			<-p.entered
			defer func() {
				close(p.release)
				if e := <-first; e != nil {
					t.Error(e)
				}
				ctx, c := context.WithTimeout(context.Background(), time.Second)
				defer c()
				if e := m.Disable(ctx, "owned"); e != nil {
					t.Error(e)
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() {
				switch operation {
				case "init":
					done <- m.Lifecycle.Init(ctx, "owned", nil)
				case "start":
					done <- m.Lifecycle.Start(ctx, "owned")
				default:
					done <- m.Lifecycle.Stop(ctx, "owned")
				}
			}()
			cancel()
			select {
			case e := <-done:
				if !errors.Is(e, context.Canceled) {
					t.Fatalf("error=%v", e)
				}
			case <-time.After(time.Second):
				t.Fatal("operation wait ignored cancellation")
			}
		})
	}
}
