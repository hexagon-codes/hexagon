package middleware

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tokenizer"
	hr "github.com/hexagon-codes/hexagon/runtime"
	"github.com/hexagon-codes/toolkit/util/reflectx"
)

var ErrCompactionConflict = errors.New("compaction candidate no longer matches the covered history")

// RequestTokenCounter 覆盖完整请求的估算，不读取累计运行用量。
type RequestTokenCounter func(llm.CompletionRequest) (int, error)

// CompactionWork 持有派发时冻结的输入；Run 不持有或写入活动 State。
// 宿主可以安排异步 Run，并负责其 context 与完成等待。
type CompactionWork struct {
	config       Compaction
	request      llm.CompletionRequest
	baseRevision uint64
	beforeTokens int
}

// CompactionCandidate 替换 [0,Watermark) 的历史；调用方取得独立候选消息。
// 采用时再次复制，不能通过候选或返回值反向修改运行中的消息。
type CompactionCandidate struct {
	BaseRevision uint64
	Watermark    int
	Replacement  []llm.Message
	BeforeTokens int
	AfterTokens  int
	original     []llm.Message
}

// Apply 实现运行宿主的上下文变换端口，版本与水位核对仍由纯采用函数完成。
func (candidate *CompactionCandidate) Apply(currentRevision uint64, currentMessages []llm.Message) ([]llm.Message, error) {
	return AdoptCompaction(candidate, currentRevision, currentMessages)
}

func cloneCompactionRequest(request llm.CompletionRequest) (llm.CompletionRequest, error) {
	messages := hr.CloneOutputMessages(request.Messages)
	// Metadata 可能携带运行时回调（例如推理回执观察器），不能按数据字段深拷贝；
	// 压缩只读取请求其余字段和消息，因此保留独立 map、共享不可变回调即可。
	metadata := request.Metadata
	request.Messages = nil
	request.Metadata = nil
	copy, err := reflectx.DeepCopyChecked(request)
	if err != nil {
		return llm.CompletionRequest{}, fmt.Errorf("clone compaction request: %w", err)
	}
	copy.Messages = messages
	if metadata != nil {
		copy.Metadata = make(map[string]any, len(metadata))
		for key, value := range metadata {
			copy.Metadata[key] = value
		}
	}
	return copy, nil
}

func (c Compaction) countRequest(request llm.CompletionRequest) (int, error) {
	if c.CountRequest != nil {
		copy, err := cloneCompactionRequest(request)
		if err != nil {
			return 0, err
		}
		tokens, err := c.CountRequest(copy)
		if err != nil {
			return 0, err
		}
		if tokens < 0 {
			return 0, fmt.Errorf("negative context token estimate")
		}
		return tokens, nil
	}
	estimate, err := tokenizer.EstimateCompletionRequest(request)
	if err != nil {
		return 0, err
	}
	if c.Count != nil {
		tokens := c.Count(hr.CloneOutputMessages(request.Messages))
		if tokens < 0 {
			return 0, fmt.Errorf("negative context token estimate")
		}
		return tokens + estimate.ToolSchemaTokens + estimate.ResponseSchemaTokens + estimate.ControlTokens, nil
	}
	return estimate.Tokens, nil
}

// Prepare 同步冻结完整请求、基准版本和消息水位；未触发时返回 nil,nil。
// 必须先 Prepare 再向后台派发，避免后台读取正在追加的消息切片。
func (c Compaction) Prepare(request llm.CompletionRequest, baseRevision uint64) (*CompactionWork, error) {
	if c.ShouldCompact == nil && c.MaxTokens <= 0 {
		return nil, nil
	}
	copy, err := cloneCompactionRequest(request)
	if err != nil {
		return nil, err
	}
	if c.ShouldCompact != nil && !c.ShouldCompact(hr.CloneOutputMessages(copy.Messages)) {
		return nil, nil
	}
	before, err := c.countRequest(copy)
	if err != nil {
		return nil, err
	}
	if c.ShouldCompact == nil && before <= c.MaxTokens {
		return nil, nil
	}
	return &CompactionWork{config: c, request: copy, baseRevision: baseRevision, beforeTokens: before}, nil
}

// Run 计算一次候选；失败不返回可采用的压缩内容，不写入活动状态或累计用量。
// 内部模型调用通过 context 中的统一调用器保留其实际用量，即使本次候选失败。
func (w *CompactionWork) Run(ctx context.Context) (*CompactionCandidate, error) {
	if w == nil {
		return nil, fmt.Errorf("compaction work is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	replacement, err := w.config.compact(ctx, w.request.Messages)
	if err != nil {
		return nil, fmt.Errorf("compact context: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	replacement = hr.CloneOutputMessages(replacement)
	afterRequest := w.request
	afterRequest.Messages = replacement
	after, err := w.config.countRequest(afterRequest)
	if err != nil {
		return nil, err
	}
	return &CompactionCandidate{BaseRevision: w.baseRevision, Watermark: len(w.request.Messages), Replacement: replacement, BeforeTokens: w.beforeTokens, AfterTokens: after, original: hr.CloneOutputMessages(w.request.Messages)}, nil
}

// AdoptCompaction 只允许未改写原前缀的版本前进，新增尾部按当前顺序保留。
// 返回冲突时不改动任何输入；重新压缩、编辑或缩短被覆盖范围都会拒绝旧候选。
func AdoptCompaction(candidate *CompactionCandidate, currentRevision uint64, current []llm.Message) ([]llm.Message, error) {
	if candidate == nil {
		return nil, fmt.Errorf("compaction candidate is nil")
	}
	if currentRevision < candidate.BaseRevision || candidate.Watermark != len(candidate.original) || len(current) < candidate.Watermark || (candidate.Watermark > 0 && !reflect.DeepEqual(current[:candidate.Watermark], candidate.original)) {
		return nil, ErrCompactionConflict
	}
	result := hr.CloneOutputMessages(candidate.Replacement)
	result = append(result, hr.CloneOutputMessages(current[candidate.Watermark:])...)
	return result, nil
}
