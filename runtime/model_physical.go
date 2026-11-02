package runtime

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
)

const UsagePartialCallsMetadataKey = "usage_partial_calls"
const UsageKnownTokenLowerBoundMetadataKey = "usage_known_token_lower_bound"

type hostAttemptHooks struct {
	observer llm.AttemptObserver
	before   llm.BeforeHTTPAttempt
}
type hostAttemptHooksKey struct{}

type physicalAttempts struct {
	ledger *llm.AttemptLedger
	mu     sync.Mutex
	err    error
}

func (p *physicalAttempts) addError(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.err = errors.Join(p.err, err)
	p.mu.Unlock()
}

func (p *physicalAttempts) failure() error { p.mu.Lock(); defer p.mu.Unlock(); return p.err }

func knownUsageLowerBound(usage llm.Usage) int {
	if sum := usage.PromptTokens + usage.CompletionTokens; sum > usage.TotalTokens {
		return sum
	}
	return usage.TotalTokens
}

func (p *physicalAttempts) knownTokenLowerBound() int {
	total := 0
	for _, receipt := range p.ledger.Snapshot() {
		if receipt.Dispatched && receipt.Usage != nil {
			total += knownUsageLowerBound(*receipt.Usage)
		}
	}
	return total
}

func (r *DefaultRunner) observePhysicalCalls(ctx context.Context, state *State, attempt ModelAttempt) (context.Context, *physicalAttempts) {
	tracker := &physicalAttempts{ledger: llm.NewAttemptLedger()}
	baseline := state.Snapshot()
	hooks, inherited := ctx.Value(hostAttemptHooksKey{}).(hostAttemptHooks)
	if !inherited {
		hooks.observer, _ = llm.AttemptObserverFromContext(ctx)
		hooks.before, _ = llm.BeforeHTTPAttemptFromContext(ctx)
	}
	ctx = context.WithValue(ctx, hostAttemptHooksKey{}, hooks)
	ctx = llm.WithParentAttemptID(ctx, attempt.ID)
	ctx = llm.WithAttemptProvider(ctx, attempt.Provider)
	ctx = llm.WithAttemptModel(ctx, attempt.RequestedModel)
	ctx = llm.WithAttemptObserver(ctx, llm.AttemptObserverFunc(func(ctx context.Context, receipt llm.AttemptReceipt) {
		tracker.addError(tracker.ledger.Merge(receipt))
		if hooks.observer != nil {
			tracker.addError(invokeExtension("host_attempt_observer", func() error { hooks.observer.OnAttempt(ctx, receipt); return nil }))
		}
	}))
	ctx = llm.WithBeforeHTTPAttempt(ctx, func(ctx context.Context, receipt llm.AttemptReceipt) error {
		if failure := tracker.failure(); failure != nil {
			return &AttemptHookError{Phase: "receipt", Err: failure}
		}
		if hooks.before != nil {
			if err := invokeExtension("host_before_http_attempt", func() error { return hooks.before(ctx, receipt) }); err != nil {
				return &AttemptHookError{Phase: "http_before", Err: err}
			}
		}
		summary := tracker.ledger.Summary()
		candidate := baseline.Snapshot()
		candidate.Usage = addUsage(baseline.Usage, summary.Usage)
		base := usageCount(baseline.Attributes, UsageKnownTokenLowerBoundMetadataKey)
		if known := knownUsageLowerBound(baseline.Usage); known > base {
			base = known
		}
		candidate.Attributes[UsageKnownTokenLowerBoundMetadataKey] = base + tracker.knownTokenLowerBound()
		physical := physicalModelAttempt(attempt, receipt)
		for _, middleware := range r.middleware {
			if policy, ok := middleware.(BeforeModelAttempt); ok {
				if err := invokeExtension("before_http_attempt", func() error { return policy.BeforeModelAttempt(ctx, candidate.Snapshot(), physical) }); err != nil {
					return &AttemptHookError{Phase: "http_before", Err: err}
				}
			}
		}
		return nil
	})
	return ctx, tracker
}

func physicalModelAttempt(parent ModelAttempt, receipt llm.AttemptReceipt) ModelAttempt {
	result := ModelAttempt{ID: receipt.AttemptID, ParentAttemptID: parent.ID, Granularity: "http_round_trip", RunID: parent.RunID, Turn: parent.Turn, Purpose: parent.Purpose, Provider: receipt.Provider, RequestedModel: receipt.RequestedModel, Model: receipt.Model, StartedAt: receipt.StartedAt, FinishedAt: receipt.FinishedAt, ProviderCalled: receipt.Dispatched, Outcome: "not_called", UsageKnown: receipt.Usage != nil, UsageFields: append([]string(nil), receipt.UsageFields...), RequestID: receipt.RequestID, ResponseID: receipt.ResponseID, Error: receipt.Error}
	if result.Model == "" {
		result.Model = receipt.RequestedModel
		result.ModelSource = "requested"
	} else {
		result.ModelSource = "reported"
	}
	result.ProviderSucceeded = receipt.Dispatched && receipt.Complete && receipt.Error == nil
	result.CancellationRequested = parent.CancellationRequested
	if receipt.Usage != nil {
		result.Usage = *receipt.Usage
	}
	if receipt.Dispatched {
		result.Outcome = "succeeded"
		if receipt.Error != nil {
			result.Outcome = "failed"
		}
	}
	fields := make(map[string]bool)
	for _, field := range receipt.UsageFields {
		fields[field] = true
	}
	result.UsagePartial = result.UsageKnown && !(fields["prompt_tokens"] && fields["completion_tokens"] && fields["total_tokens"])
	return result
}

func (p *physicalAttempts) receipts() []llm.AttemptReceipt {
	receipts := p.ledger.Snapshot()
	sort.SliceStable(receipts, func(i, j int) bool {
		if receipts[i].StartedAt.Equal(receipts[j].StartedAt) {
			return receipts[i].AttemptID < receipts[j].AttemptID
		}
		return receipts[i].StartedAt.Before(receipts[j].StartedAt)
	})
	return receipts
}
