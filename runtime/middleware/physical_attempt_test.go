package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/llm/openai"
	hr "github.com/hexagon-codes/hexagon/runtime"
)

func TestPhysicalAttemptsChargeFailedAndSuccessfulHTTPExactlyOnce(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"id":"first","model":"actual-first","error":{"message":"temporary"},"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
			return
		}
		fmt.Fprint(w, `{"id":"second","model":"actual-final","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`)
	}))
	defer server.Close()
	provider := openai.New("synthetic", openai.WithBaseURL(server.URL), openai.WithHTTPClient(server.Client()))
	charges := make(map[string]int)
	ids := make(map[string]bool)
	legacyCalls := 0
	ledger := llm.NewAttemptLedger()
	var observerErr error
	ctx := llm.WithAttemptObserver(t.Context(), llm.AttemptObserverFunc(func(_ context.Context, receipt llm.AttemptReceipt) {
		if err := ledger.Merge(receipt); err != nil {
			observerErr = err
		}
	}))
	runner := hr.NewRunner(hr.Config{ProviderSelector: hr.StaticProviderSelector{Provider: provider, Model: "requested"}, Middleware: []hr.Middleware{CostControl{
		Record: func(string, llm.Usage) error { legacyCalls++; return nil },
		RecordAttempt: func(_ context.Context, attempt hr.ModelAttempt) error {
			if attempt.ID == "" || attempt.ParentAttemptID == "" || attempt.Granularity != "http_round_trip" {
				t.Fatalf("missing persistent attempt identity: %+v", attempt)
			}
			if ids[attempt.ID] {
				t.Fatalf("same physical fact charged twice: %s", attempt.ID)
			}
			ids[attempt.ID] = true
			charges[attempt.Model] += attempt.Usage.TotalTokens
			return nil
		},
	}}})
	result, err := runner.Run(ctx, hr.Request{ID: "physical-cost"})
	if err != nil || observerErr != nil || requests.Load() != 2 || result == nil || result.Usage.TotalTokens != 21 || charges["actual-first"] != 7 || charges["actual-final"] != 14 || len(charges) != 2 || ledger.Summary().Usage.TotalTokens != 21 || len(ids) != 2 || legacyCalls != 0 {
		t.Fatalf("physical accounting mismatch: result=%+v charges=%v requests=%d error=%v observer=%v", result, charges, requests.Load(), err, observerErr)
	}
	if result.Metadata[hr.UsageReportedCallsMetadataKey] != 2 || result.Metadata[hr.UsageMissingCallsMetadataKey] != 0 {
		t.Fatalf("logical calls substituted for physical receipts: %v", result.Metadata)
	}
}

func TestPhysicalRetryChecksExistingBudgetBeforeDispatch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"id":"failed","model":"actual-first","error":{"message":"temporary"},"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
	}))
	defer server.Close()
	provider := openai.New("synthetic", openai.WithBaseURL(server.URL), openai.WithHTTPClient(server.Client()))
	charged := 0
	runner := hr.NewRunner(hr.Config{ProviderSelector: hr.StaticProviderSelector{Provider: provider, Model: "requested"}, Middleware: []hr.Middleware{NewBudgetControl(BudgetControlConfig{Limits: BudgetLimits{MaxTokens: 7}, Record: func(_ string, usage llm.Usage) error { charged += usage.TotalTokens; return nil }})}})
	result, err := runner.Run(t.Context(), hr.Request{ID: "physical-budget"})
	if !errors.Is(err, hr.ErrBudgetExceeded) || requests.Load() != 1 || charged != 7 || result == nil || result.Usage.TotalTokens != 7 || result.StopReason != "" {
		t.Fatalf("retry escaped budget or lost usage: result=%+v requests=%d charged=%d error=%v", result, requests.Load(), charged, err)
	}
}

func TestPhysicalPartialUsageKeepsUnknownTotalAndChecksKnownLowerBound(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"id":"partial","model":"actual","error":{"message":"temporary"},"usage":{"prompt_tokens":7}}`)
	}))
	defer server.Close()
	provider := openai.New("synthetic", openai.WithBaseURL(server.URL), openai.WithHTTPClient(server.Client()))
	chargedPrompt := 0
	runner := hr.NewRunner(hr.Config{ProviderSelector: hr.StaticProviderSelector{Provider: provider, Model: "requested"}, Middleware: []hr.Middleware{NewBudgetControl(BudgetControlConfig{Limits: BudgetLimits{MaxTokens: 7}, RecordAttempt: func(_ context.Context, attempt hr.ModelAttempt) error {
		if !attempt.UsagePartial {
			t.Fatalf("partial fields presented as complete: %+v", attempt)
		}
		chargedPrompt += attempt.Usage.PromptTokens
		return nil
	}})}})
	result, err := runner.Run(t.Context(), hr.Request{ID: "partial-budget"})
	if !errors.Is(err, hr.ErrBudgetExceeded) || requests.Load() != 1 || chargedPrompt != 7 || result == nil || result.Usage.PromptTokens != 7 || result.Usage.TotalTokens != 0 || result.Metadata[hr.UsagePartialCallsMetadataKey] != 1 || result.Metadata[hr.UsageSourceMetadataKey] != "partial" || result.Metadata[hr.UsageKnownTokenLowerBoundMetadataKey] != 7 {
		t.Fatalf("partial usage was zeroed, guessed or allowed to escape budget: result=%+v requests=%d charged=%d error=%v", result, requests.Load(), chargedPrompt, err)
	}
}
