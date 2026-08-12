package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"testing"
	"time"
)

type capturePushService struct {
	calls        int
	notification *PushNotification
}

func (s *capturePushService) Push(_ context.Context, _ *PushNotificationConfig, notification *PushNotification) error {
	s.calls++
	s.notification = notification
	return nil
}

func mustTaskNotification(t *testing.T, taskID string) *PushNotification {
	t.Helper()
	notification, err := NewTaskStatusNotification(NewTask(taskID))
	if err != nil {
		t.Fatalf("NewTaskStatusNotification() error = %v", err)
	}
	return notification
}

func TestPushArtifactDeliversArtifactNotificationWithoutPanic(t *testing.T) {
	service := &capturePushService{}
	manager, err := NewPushManager(service)
	if err != nil {
		t.Fatalf("NewPushManager() error = %v", err)
	}
	manager.SetConfig("task-artifact", &PushNotificationConfig{URL: "https://example.invalid/push"})

	artifact := &Artifact{Name: "report", Parts: []Part{&TextPart{Text: "ready"}}}
	if err := manager.PushArtifact(context.Background(), "task-artifact", artifact); err != nil {
		t.Fatalf("PushArtifact() error = %v", err)
	}
	if service.calls != 1 {
		t.Fatalf("PushArtifact() calls = %d, want 1", service.calls)
	}
	if service.notification == nil || service.notification.EventType() != EventTypeArtifact {
		t.Fatalf("PushArtifact() notification = %v, want artifact event", service.notification)
	}
	gotArtifact, err := service.notification.Artifact()
	if err != nil {
		t.Fatalf("notification.Artifact() error = %v", err)
	}
	if gotArtifact == nil || gotArtifact.Name != "report" || gotArtifact.GetTextContent() != "ready" {
		t.Fatalf("notification.Artifact() = %+v, want report artifact", gotArtifact)
	}
}

func TestPushNotificationSnapshotsSourceValues(t *testing.T) {
	task := NewTask("task-original")
	task.Status.State = TaskStateWorking
	taskNotification, err := NewTaskStatusNotification(task)
	if err != nil {
		t.Fatalf("NewTaskStatusNotification() error = %v", err)
	}
	task.ID = "task-mutated"
	task.Status.State = TaskStateFailed

	taskJSON, err := json.Marshal(taskNotification)
	if err != nil {
		t.Fatalf("json.Marshal(task notification) error = %v", err)
	}
	var taskWire struct {
		TaskID string `json:"taskId"`
		Task   *Task  `json:"task"`
	}
	if err := json.Unmarshal(taskJSON, &taskWire); err != nil {
		t.Fatalf("json.Unmarshal(task notification) error = %v", err)
	}
	if taskWire.TaskID != "task-original" || taskWire.Task == nil || taskWire.Task.ID != "task-original" {
		t.Fatalf("task notification changed with source mutation: %s", taskJSON)
	}
	if taskWire.Task.Status.State != TaskStateWorking {
		t.Fatalf("task notification state = %q, want %q", taskWire.Task.Status.State, TaskStateWorking)
	}

	artifact := &Artifact{Name: "artifact-original", Metadata: map[string]any{"version": "v1"}}
	artifactNotification, err := NewArtifactNotification("task-artifact", artifact)
	if err != nil {
		t.Fatalf("NewArtifactNotification() error = %v", err)
	}
	artifact.Name = "artifact-mutated"
	artifact.Metadata["version"] = "v2"

	artifactJSON, err := json.Marshal(artifactNotification)
	if err != nil {
		t.Fatalf("json.Marshal(artifact notification) error = %v", err)
	}
	var artifactWire struct {
		Artifact *Artifact `json:"artifact"`
	}
	if err := json.Unmarshal(artifactJSON, &artifactWire); err != nil {
		t.Fatalf("json.Unmarshal(artifact notification) error = %v", err)
	}
	if artifactWire.Artifact == nil || artifactWire.Artifact.Name != "artifact-original" {
		t.Fatalf("artifact notification changed with source mutation: %s", artifactJSON)
	}
	if artifactWire.Artifact.Metadata["version"] != "v1" {
		t.Fatalf("artifact notification metadata = %v, want version v1", artifactWire.Artifact.Metadata)
	}
}

func TestPushManagerStoresAndReturnsConfigSnapshots(t *testing.T) {
	manager, err := NewPushManager(&capturePushService{})
	if err != nil {
		t.Fatalf("NewPushManager() error = %v", err)
	}

	config := &PushNotificationConfig{
		URL:   "https://original.invalid/push",
		Token: "original-token",
		Authentication: &PushAuth{
			Schemes:     []string{"bearer"},
			Credentials: "original-credentials",
		},
	}
	manager.SetConfig("task-config", config)
	config.URL = "https://mutated.invalid/push"
	config.Authentication.Schemes[0] = "mutated"

	first, ok := manager.GetConfig("task-config")
	if !ok {
		t.Fatal("GetConfig() found = false, want true")
	}
	if first.URL != "https://original.invalid/push" || first.Authentication.Schemes[0] != "bearer" {
		t.Fatalf("stored config changed with source mutation: %+v", first)
	}

	first.URL = "https://escaped.invalid/push"
	first.Authentication.Credentials = "escaped-credentials"
	second, ok := manager.GetConfig("task-config")
	if !ok {
		t.Fatal("GetConfig() second lookup found = false, want true")
	}
	if second.URL != "https://original.invalid/push" || second.Authentication.Credentials != "original-credentials" {
		t.Fatalf("GetConfig() exposed mutable internal state: %+v", second)
	}
}

func TestPushManagerCanceledContextDoesNotConsumeRateToken(t *testing.T) {
	service := &capturePushService{}
	manager, err := NewPushManager(service, WithRateLimit(1, time.Hour))
	if err != nil {
		t.Fatalf("NewPushManager() error = %v", err)
	}
	manager.SetConfig("task-context", &PushNotificationConfig{URL: "https://example.invalid/push"})

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.PushTask(canceled, NewTask("task-context")); !errors.Is(err, context.Canceled) {
		t.Fatalf("PushTask(canceled) error = %v, want context.Canceled", err)
	}
	if err := manager.PushTask(context.Background(), NewTask("task-context")); err != nil {
		t.Fatalf("PushTask(live) error = %v; canceled request consumed the token", err)
	}
	if service.calls != 1 {
		t.Fatalf("Push() calls = %d, want 1", service.calls)
	}
}

func TestPushNotificationConstructorsRejectInvalidInput(t *testing.T) {
	if notification, err := NewTaskStatusNotification(nil); notification != nil || !errors.Is(err, ErrInvalidPushNotification) {
		t.Fatalf("NewTaskStatusNotification(nil) = (%v, %v), want nil ErrInvalidPushNotification", notification, err)
	}
	if notification, err := NewTaskStatusNotification(&Task{}); notification != nil || !errors.Is(err, ErrInvalidPushNotification) {
		t.Fatalf("NewTaskStatusNotification(empty ID) = (%v, %v), want nil ErrInvalidPushNotification", notification, err)
	}
	if notification, err := NewArtifactNotification("", &Artifact{}); notification != nil || !errors.Is(err, ErrInvalidPushNotification) {
		t.Fatalf("NewArtifactNotification(empty ID) = (%v, %v), want nil ErrInvalidPushNotification", notification, err)
	}
	if notification, err := NewArtifactNotification("task", nil); notification != nil || !errors.Is(err, ErrInvalidPushNotification) {
		t.Fatalf("NewArtifactNotification(nil artifact) = (%v, %v), want nil ErrInvalidPushNotification", notification, err)
	}
}

func TestNewWebhookPushServiceRejectsInvalidDependencies(t *testing.T) {
	tests := []struct {
		name string
		opts []WebhookPushOption
	}{
		{name: "nil option", opts: []WebhookPushOption{nil}},
		{name: "nil HTTP client", opts: []WebhookPushOption{WithPushHTTPClient(nil)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewWebhookPushService(tt.opts...)
			if service != nil {
				t.Fatalf("NewWebhookPushService() service = %v, want nil", service)
			}
			if !errors.Is(err, ErrInvalidWebhookPushConfig) {
				t.Fatalf("NewWebhookPushService() error = %v, want ErrInvalidWebhookPushConfig", err)
			}
		})
	}
}

func TestNewWebhookPushServiceValidatesRetryConfig(t *testing.T) {
	tests := []struct {
		name   string
		config WebhookRetryConfig
	}{
		{name: "negative retries", config: WebhookRetryConfig{MaxRetries: -1, MaxDelay: time.Second, Multiplier: 1}},
		{name: "retries above hard limit", config: WebhookRetryConfig{MaxRetries: 11, MaxDelay: time.Second, Multiplier: 1}},
		{name: "negative initial delay", config: WebhookRetryConfig{InitialDelay: -time.Nanosecond, MaxDelay: time.Second, Multiplier: 1}},
		{name: "zero max delay", config: WebhookRetryConfig{MaxDelay: 0, Multiplier: 1}},
		{name: "negative max delay", config: WebhookRetryConfig{MaxDelay: -time.Nanosecond, Multiplier: 1}},
		{name: "max below initial", config: WebhookRetryConfig{InitialDelay: time.Second, MaxDelay: time.Millisecond, Multiplier: 1}},
		{name: "zero multiplier", config: WebhookRetryConfig{MaxDelay: time.Second}},
		{name: "NaN multiplier", config: WebhookRetryConfig{MaxDelay: time.Second, Multiplier: math.NaN()}},
		{name: "infinite multiplier", config: WebhookRetryConfig{MaxDelay: time.Second, Multiplier: math.Inf(1)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewWebhookPushService(WithWebhookRetry(tt.config))
			if service != nil {
				t.Fatalf("NewWebhookPushService() service = %v, want nil", service)
			}
			if !errors.Is(err, ErrInvalidWebhookPushConfig) {
				t.Fatalf("NewWebhookPushService() error = %v, want ErrInvalidWebhookPushConfig", err)
			}
		})
	}

	service, err := NewWebhookPushService(
		WithPushHTTPClient(&http.Client{Timeout: time.Second}),
		WithWebhookRetry(WebhookRetryConfig{MaxRetries: 10, MaxDelay: time.Millisecond, Multiplier: 1}),
	)
	if err != nil || service == nil {
		t.Fatalf("NewWebhookPushService(valid boundary) = (%v, %v)", service, err)
	}
}

func TestNewAsyncPushServiceRejectsInvalidConfig(t *testing.T) {
	var typedNil *capturePushService
	tests := []struct {
		name       string
		underlying PushService
		queueSize  int
		workers    int
	}{
		{name: "nil underlying", underlying: nil, queueSize: 1, workers: 1},
		{name: "typed nil underlying", underlying: typedNil, queueSize: 1, workers: 1},
		{name: "zero queue", underlying: &capturePushService{}, queueSize: 0, workers: 1},
		{name: "negative queue", underlying: &capturePushService{}, queueSize: -1, workers: 1},
		{name: "zero workers", underlying: &capturePushService{}, queueSize: 1, workers: 0},
		{name: "negative workers", underlying: &capturePushService{}, queueSize: 1, workers: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewAsyncPushService(tt.underlying, tt.queueSize, tt.workers)
			if service != nil {
				t.Fatalf("NewAsyncPushService() service = %v, want nil", service)
			}
			if !errors.Is(err, ErrInvalidAsyncPushConfig) {
				t.Fatalf("NewAsyncPushService() error = %v, want ErrInvalidAsyncPushConfig", err)
			}
		})
	}
}

func TestNewDefaultPushServiceReturnsValidatedConcreteService(t *testing.T) {
	service, err := NewDefaultPushService()
	if err != nil {
		t.Fatalf("NewDefaultPushService() error = %v", err)
	}
	if service == nil {
		t.Fatal("NewDefaultPushService() service = nil")
	}
	service.Close()
}

type noOpPushService struct{}

func (noOpPushService) Push(context.Context, *PushNotificationConfig, *PushNotification) error {
	return nil
}

func newAsyncPushServiceForTest(t *testing.T, underlying PushService, queueSize, workers int) *AsyncPushService {
	t.Helper()
	service, err := NewAsyncPushService(underlying, queueSize, workers)
	if err != nil {
		t.Fatalf("NewAsyncPushService() error = %v", err)
	}
	return service
}

func TestAsyncPushServiceCloseIsIdempotent(t *testing.T) {
	service := newAsyncPushServiceForTest(t, noOpPushService{}, 1, 1)
	service.Close()
	service.Close()
}

func TestAsyncPushServiceRejectsPushAfterClose(t *testing.T) {
	service := newAsyncPushServiceForTest(t, noOpPushService{}, 1, 1)
	service.Close()

	err := service.Push(
		context.Background(),
		&PushNotificationConfig{URL: "https://example.invalid/push"},
		mustTaskNotification(t, "task-closed"),
	)
	if !errors.Is(err, ErrPushServiceClosed) {
		t.Fatalf("Push() error = %v, want ErrPushServiceClosed", err)
	}
}

func TestAsyncPushServiceRejectsCanceledContext(t *testing.T) {
	service := newAsyncPushServiceForTest(t, noOpPushService{}, 1, 1)
	defer service.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := service.Push(
		ctx,
		&PushNotificationConfig{URL: "https://example.invalid/push"},
		mustTaskNotification(t, "task-canceled"),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Push(canceled) error = %v, want context.Canceled", err)
	}
}

func TestAsyncPushServiceConcurrentPushAndCloseDoesNotPanic(t *testing.T) {
	service := newAsyncPushServiceForTest(t, noOpPushService{}, 64, 4)
	start := make(chan struct{})
	panicErrors := make(chan any, 64)
	unexpectedErrors := make(chan error, 64)

	var workers sync.WaitGroup
	for index := 0; index < 64; index++ {
		notification := mustTaskNotification(t, fmt.Sprintf("task-%d", index))
		workers.Add(1)
		go func(notification *PushNotification) {
			defer workers.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					panicErrors <- recovered
				}
			}()
			<-start
			err := service.Push(
				context.Background(),
				&PushNotificationConfig{URL: "https://example.invalid/push"},
				notification,
			)
			if err != nil && !errors.Is(err, ErrPushServiceClosed) && !errors.Is(err, ErrPushQueueFull) {
				unexpectedErrors <- err
			}
		}(notification)
	}

	close(start)
	var closers sync.WaitGroup
	for range 8 {
		closers.Add(1)
		go func() {
			defer closers.Done()
			service.Close()
		}()
	}
	workers.Wait()
	closers.Wait()
	close(panicErrors)
	close(unexpectedErrors)

	for recovered := range panicErrors {
		t.Errorf("Push/Close panicked: %v", recovered)
	}
	for err := range unexpectedErrors {
		t.Errorf("Push() unexpected error = %v", err)
	}
}
