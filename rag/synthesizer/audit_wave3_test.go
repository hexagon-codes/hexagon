package synthesizer

// audit_wave3_test.go 是 RAG 合成链路的全场景挑刺测试。
//
// 覆盖维度：
//   - 正常路径：各合成器在 mock LLM 下的输出与元数据
//   - 边界：空/超长/Unicode/0/负数/单文档/并发
//   - 错误路径：LLM 失败、检索器/LLM 未配置
//   - 状态一致性：doc_count、tree_levels、answer_count、context_length 元数据
//   - 未闭环逻辑：IncrementalSynthesizer 接口不一致、maxDocs 截断后 batch 残留、
//     CompactSynthesizer 字节/rune 混用导致的截断语义错误

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/hexagon/testing/mock"
)

// makeDocs 构造 n 个内容固定的文档
func makeDocs(contents ...string) []rag.Document {
	docs := make([]rag.Document, 0, len(contents))
	for i, c := range contents {
		docs = append(docs, rag.Document{ID: string(rune('a' + i)), Content: c, Source: "src-" + c})
	}
	return docs
}

// ============== CompactSynthesizer 截断语义（疑似 bug）==============

// TestCompactSynthesizer_TruncationByteVsRune 验证截断判定与截断动作统一用 rune 度量，
// 不再出现"纯 ASCII 把 maxContextLength 当字符数、中文则放行超限字节"的不一致；
// 同时 metadata.context_length 报告的 rune 数必须不超过 maxContextLength。
// 回归: W3-40 — 字节判定长度 / rune 截断不一致，统一为 rune 度量。
func TestCompactSynthesizer_TruncationByteVsRune(t *testing.T) {
	const maxLen = 10

	t.Run("ASCII 超长按 rune 截断到 maxLen 字符", func(t *testing.T) {
		s := NewCompactSynthesizer(WithCompactSynthesizerMaxContext(maxLen))
		// 30 字节 ASCII，应被截断
		docs := makeDocs(strings.Repeat("x", 30))
		resp, err := s.Synthesize(context.Background(), "q", docs)
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		// 占位响应里包含截断后的上下文，应为 10 个 x
		if !strings.Contains(resp.Content, strings.Repeat("x", maxLen)) ||
			strings.Contains(resp.Content, strings.Repeat("x", maxLen+1)) {
			t.Errorf("ASCII 截断应保留恰好 %d 个字符，内容: %q", maxLen, resp.Content)
		}
	})

	t.Run("中文内容 context_length(rune) 不得超过 maxContextLength", func(t *testing.T) {
		s := NewCompactSynthesizer(
			WithCompactSynthesizerMaxContext(maxLen),
			WithCompactSynthesizerLLM(mock.FixedProvider("ok")),
		)
		// 20 个中文字符 = 60 字节(UTF-8)。统一 rune 度量后应截断到 10 个字符。
		content := strings.Repeat("中", 20)
		docs := makeDocs(content)
		resp, err := s.Synthesize(context.Background(), "q", docs)
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		cl, _ := resp.Metadata["context_length"].(int)
		// context_length 必须是 rune 数且不超过声明的 maxContextLength。
		if cl > maxLen {
			t.Errorf("context_length(rune)=%d 超过声明的 maxContextLength=%d，"+
				"截断判定与动作需统一用 rune 度量", cl, maxLen)
		}
		if cl != maxLen {
			t.Errorf("20 个中文应截断到恰好 %d 个字符，实际 context_length=%d", maxLen, cl)
		}
	})
}

// TestCompactSynthesizer_TruncationProducesValidUTF8 截断后内容必须是合法 UTF-8
func TestCompactSynthesizer_TruncationProducesValidUTF8(t *testing.T) {
	s := NewCompactSynthesizer(WithCompactSynthesizerMaxContext(5))
	docs := makeDocs(strings.Repeat("世界你好啊", 5)) // 多字节
	resp, err := s.Synthesize(context.Background(), "问题", docs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if !utf8.ValidString(resp.Content) {
		t.Errorf("截断后内容不是合法 UTF-8: %q", resp.Content)
	}
}

// ============== IncrementalSynthesizer 接口不一致（疑似 bug）==============

// TestIncrementalSynthesizer_NotASynthesizer 验证 IncrementalSynthesizer
// 并未实现包级 Synthesizer 接口：其 Synthesize 返回 (string, error) 且无 opts 参数，
// 与接口要求的 (*Response, error, opts...SynthesizeOption) 不符。
// streaming.go 文件头注释声称"实现 Synthesizer 接口"，但缺少静态断言，
// 这是接口契约的功能未闭环。
func TestIncrementalSynthesizer_NotASynthesizer(t *testing.T) {
	var i any = NewIncrementalSynthesizer()
	if _, ok := i.(Synthesizer); ok {
		t.Errorf("IncrementalSynthesizer 意外实现了 Synthesizer 接口（实现已修正）")
	} else {
		t.Log("BUG 佐证: IncrementalSynthesizer.Synthesize 返回 (string,error) 无 opts，" +
			"未实现包级 Synthesizer 接口，但 streaming.go 注释声称'实现 Synthesizer 接口'")
	}

	// 同理 PipelineSynthesizer
	var p any = NewPipelineSynthesizer()
	if _, ok := p.(Synthesizer); ok {
		t.Errorf("PipelineSynthesizer 意外实现了 Synthesizer 接口（实现已修正）")
	} else {
		t.Log("BUG 佐证: PipelineSynthesizer 同样未实现 Synthesizer 接口")
	}
}

// TestIncrementalSynthesizer_SynthesizeNilLLM LLM 未配置时返回错误
func TestIncrementalSynthesizer_SynthesizeNilLLM(t *testing.T) {
	s := NewIncrementalSynthesizer()
	_, err := s.Synthesize(context.Background(), "q", makeDocs("a"))
	if err == nil {
		t.Fatal("LLM 未配置应返回错误")
	}
}

// TestIncrementalSynthesizer_SynthesizeRespectsMaxDocs maxDocs 限制上下文
func TestIncrementalSynthesizer_SynthesizeRespectsMaxDocs(t *testing.T) {
	llmMock := mock.FixedProvider("ans")
	s := NewIncrementalSynthesizer(
		WithIncrementalLLM(llmMock),
		WithIncrementalMaxDocs(2),
	)
	_, err := s.Synthesize(context.Background(), "q", makeDocs("D1", "D2", "D3", "D4"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	last := llmMock.LastCall()
	if last == nil {
		t.Fatal("没有调用记录")
	}
	prompt := last.Messages[len(last.Messages)-1].Content
	// 仅前 2 个文档应进入 prompt
	if !strings.Contains(prompt, "D1") || !strings.Contains(prompt, "D2") {
		t.Errorf("prompt 应含前两文档: %q", prompt)
	}
	if strings.Contains(prompt, "D3") || strings.Contains(prompt, "D4") {
		t.Errorf("maxDocs=2 应排除 D3/D4，prompt: %q", prompt)
	}
}

// ============== SynthesizeStream maxDocs 后 batch 残留（疑似 bug）==============

// TestIncrementalStream_BatchLeakAfterMaxDocs 当文档数超过 maxDocs 后，
// 代码 `continue` 跳过合成，但此前 batch 已经 append 过该文档且永不重置，
// 同时新文档也不再触发增量。配合最终关闭分支 `iteration==0` 的判定，
// 可能导致超出 maxDocs 的文档既不参与增量、最终输出也不反映它们。
// 本测试验证：超过 maxDocs 后到达的文档不会触发新的 partial chunk。
// 回归: W3-39 — 越界时停止收集，新文档不再泄漏进 allDocs/batch。
func TestIncrementalStream_BatchLeakAfterMaxDocs(t *testing.T) {
	// batchSize=2, maxDocs=2：第 2 个文档触发一次增量(iteration=1)，
	// 第 3、4 个文档因 len(allDocs) > maxDocs 全部 continue。
	llmMock := mock.SequenceProvider("p1", "p2", "p3", "p4", "p5", "p6")
	s := NewIncrementalSynthesizer(
		WithIncrementalLLM(llmMock),
		WithIncrementalBatchSize(2),
		WithIncrementalMaxDocs(2),
	)

	docChan := make(chan rag.Document)
	out, err := s.SynthesizeStream(context.Background(), "q", docChan)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	go func() {
		for _, d := range makeDocs("A", "B", "C", "D") {
			docChan <- d
		}
		close(docChan)
	}()

	var partials, finals int
	var finalSourceDocs int
	for chunk := range out {
		if chunk.IsPartial {
			partials++
		} else {
			finals++
			finalSourceDocs = chunk.SourceDocs
		}
	}

	// 第 2 个文档触发 1 次 partial。第 3、4 文档应被丢弃（不增量）。
	// 关闭时 iteration!=0 且 currentAnswer!="" → 发 1 次 final（重复上一次 partial 内容）。
	if partials != 1 {
		t.Errorf("期望恰好 1 个 partial chunk（仅 batch 满那次），实际 %d", partials)
	}
	if finals != 1 {
		t.Errorf("期望 1 个 final chunk，实际 %d", finals)
	}
	// W3-39: 越界文档不泄漏，final 的 SourceDocs 必须精确等于 maxDocs(2)，
	// 修复前会因 allDocs 继续追加 C/D 而虚高为 4。
	if finalSourceDocs != 2 {
		t.Errorf("final SourceDocs 应精确等于 maxDocs=2（越界文档不泄漏），实际 %d", finalSourceDocs)
	}
	t.Logf("W3-39 修复后: 越界文档在收集阶段即被丢弃，不泄漏进 allDocs/batch；"+
		"partials=%d、finals=%d，且 final 的 SourceDocs 精确等于 maxDocs",
		partials, finals)
}

// TestIncrementalStream_FewerThanBatchTriggersFinalSynthesis
// 文档数 < batchSize 时，关闭通道应触发一次完整合成
func TestIncrementalStream_FewerThanBatchTriggersFinalSynthesis(t *testing.T) {
	llmMock := mock.FixedProvider("final-answer")
	s := NewIncrementalSynthesizer(
		WithIncrementalLLM(llmMock),
		WithIncrementalBatchSize(5),
	)
	docChan := make(chan rag.Document)
	out, err := s.SynthesizeStream(context.Background(), "q", docChan)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	go func() {
		docChan <- makeDocs("only-one")[0]
		close(docChan)
	}()

	var got []StreamChunk
	for c := range out {
		got = append(got, c)
	}
	if len(got) != 1 {
		t.Fatalf("期望 1 个最终 chunk，实际 %d", len(got))
	}
	if got[0].IsPartial {
		t.Error("唯一 chunk 应为最终结果(IsPartial=false)")
	}
	if got[0].Text != "final-answer" {
		t.Errorf("内容应为 final-answer，实际 %q", got[0].Text)
	}
}

// TestIncrementalStream_NilLLMError LLM 未配置时直接返回错误
func TestIncrementalStream_NilLLMError(t *testing.T) {
	s := NewIncrementalSynthesizer()
	_, err := s.SynthesizeStream(context.Background(), "q", make(chan rag.Document))
	if err == nil {
		t.Fatal("LLM 未配置应返回错误")
	}
}

// TestIncrementalStream_ContextCancel 上下文取消后 goroutine 应退出且 chan 关闭
func TestIncrementalStream_ContextCancel(t *testing.T) {
	llmMock := mock.FixedProvider("x")
	s := NewIncrementalSynthesizer(WithIncrementalLLM(llmMock), WithIncrementalBatchSize(2))
	ctx, cancel := context.WithCancel(context.Background())
	docChan := make(chan rag.Document)
	out, err := s.SynthesizeStream(ctx, "q", docChan)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	cancel() // 立即取消

	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("上下文取消后输出通道未关闭，疑似 goroutine 泄漏")
	}
	_ = docChan
}

// ============== Refine：迭代与错误路径 ==============

func TestRefineSynthesizer_IterativeCallCount(t *testing.T) {
	llmMock := mock.SequenceProvider("a1", "a2", "a3")
	s := NewRefineSynthesizer(WithRefineSynthesizerLLM(llmMock))
	resp, err := s.Synthesize(context.Background(), "q", makeDocs("d1", "d2", "d3"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if llmMock.CallCount() != 3 {
		t.Errorf("3 个文档应调用 3 次 LLM，实际 %d", llmMock.CallCount())
	}
	if resp.Content != "a3" {
		t.Errorf("最终答案应为最后一次精炼 a3，实际 %q", resp.Content)
	}
	if resp.Metadata["total_tokens"].(int) != 300 {
		t.Errorf("total_tokens 应累加为 300，实际 %v", resp.Metadata["total_tokens"])
	}
}

func TestRefineSynthesizer_LLMErrorOnFirstDoc(t *testing.T) {
	llmMock := mock.ErrorProvider(errors.New("boom"))
	s := NewRefineSynthesizer(WithRefineSynthesizerLLM(llmMock))
	_, err := s.Synthesize(context.Background(), "q", makeDocs("d1", "d2"))
	if err == nil {
		t.Fatal("LLM 错误应被传播")
	}
	if !strings.Contains(err.Error(), "LLM 调用失败") {
		t.Errorf("错误应被包装为 'LLM 调用失败'，实际: %v", err)
	}
}

// TestRefineSynthesizer_InitialPromptPlaceholderReplaced
// 验证首个文档同时替换了 {context} 和 {query}
func TestRefineSynthesizer_InitialPromptPlaceholderReplaced(t *testing.T) {
	llmMock := mock.FixedProvider("ans")
	s := NewRefineSynthesizer(WithRefineSynthesizerLLM(llmMock))
	_, err := s.Synthesize(context.Background(), "我的问题", makeDocs("我的上下文"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	prompt := llmMock.Calls()[0].Messages[0].Content
	if strings.Contains(prompt, "{context}") || strings.Contains(prompt, "{query}") {
		t.Errorf("初始 prompt 占位符未被替换: %q", prompt)
	}
	if !strings.Contains(prompt, "我的问题") || !strings.Contains(prompt, "我的上下文") {
		t.Errorf("初始 prompt 缺少 query/context: %q", prompt)
	}
}

// TestRefineSynthesizer_RefinePromptMissingQuery 验证精炼提示词模板含 {query} 占位符，
// 从第二个文档起后续精炼轮次仍注入用户原始问题。
// 多文档场景中，LLM 在精炼阶段必须看到原始问题，否则会偏离用户意图。
// 回归: W3-42 — refinePrompt 模板缺 {query} 占位符，第二个文档起丢失用户原始问题。
func TestRefineSynthesizer_RefinePromptMissingQuery(t *testing.T) {
	llmMock := mock.SequenceProvider("first", "refined")
	s := NewRefineSynthesizer(WithRefineSynthesizerLLM(llmMock))
	_, err := s.Synthesize(context.Background(), "唯一标识查询XYZ", makeDocs("d1", "d2"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	// 第二轮（精炼）的 prompt 必须包含原始 query
	refinePrompt := llmMock.Calls()[1].Messages[0].Content
	if !strings.Contains(refinePrompt, "唯一标识查询XYZ") {
		t.Errorf("精炼轮 prompt 应含原始 query，refinePrompt=%q。"+
			"refinePrompt 模板需有 {query} 占位符，否则多文档精炼时丢失用户问题上下文", refinePrompt)
	}
}

// ============== Tree：层级、chunkSize 守卫 ==============

func TestTreeSummarize_ChunkSizeGuardMinimum(t *testing.T) {
	// chunkSize=1 会被矫正为 2，避免无限循环
	s := NewTreeSummarizeSynthesizer(WithTreeSynthesizerChunkSize(1))
	if s.chunkSize != 2 {
		t.Errorf("chunkSize=1 应被矫正为 2，实际 %d", s.chunkSize)
	}
	// 负数同样矫正
	s2 := NewTreeSummarizeSynthesizer(WithTreeSynthesizerChunkSize(-5))
	if s2.chunkSize != 2 {
		t.Errorf("负数 chunkSize 应被矫正为 2，实际 %d", s2.chunkSize)
	}
}

func TestTreeSummarize_TreeLevelsAndTermination(t *testing.T) {
	// 5 文档, chunkSize=2: L1: 5->3, L2: 3->2, L3: 2->1, 共 3 层; 最后 1 次 final
	llmMock := mock.FixedProvider("s")
	s := NewTreeSummarizeSynthesizer(
		WithTreeSynthesizerLLM(llmMock),
		WithTreeSynthesizerChunkSize(2),
	)
	resp, err := s.Synthesize(context.Background(), "q", makeDocs("1", "2", "3", "4", "5"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if lvl, _ := resp.Metadata["tree_levels"].(int); lvl != 3 {
		t.Errorf("5 文档 chunkSize=2 应为 3 层，实际 %v", resp.Metadata["tree_levels"])
	}
	// 调用次数: L1=3, L2=2, L3=1, final=1 -> 7
	if llmMock.CallCount() != 7 {
		t.Errorf("期望 7 次 LLM 调用，实际 %d", llmMock.CallCount())
	}
}

func TestTreeSummarize_SingleDocNoMergeLevels(t *testing.T) {
	// 单文档：不进入合并循环(level=0)，直接 final 一次
	llmMock := mock.FixedProvider("answer")
	s := NewTreeSummarizeSynthesizer(WithTreeSynthesizerLLM(llmMock))
	resp, err := s.Synthesize(context.Background(), "q", makeDocs("only"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if lvl, _ := resp.Metadata["tree_levels"].(int); lvl != 0 {
		t.Errorf("单文档应为 0 层，实际 %v", resp.Metadata["tree_levels"])
	}
	if llmMock.CallCount() != 1 {
		t.Errorf("单文档应仅 final 一次调用，实际 %d", llmMock.CallCount())
	}
}

func TestTreeSummarize_LLMErrorPropagates(t *testing.T) {
	llmMock := mock.ErrorProvider(errors.New("tree-fail"))
	s := NewTreeSummarizeSynthesizer(WithTreeSynthesizerLLM(llmMock))
	_, err := s.Synthesize(context.Background(), "q", makeDocs("1", "2", "3"))
	if err == nil {
		t.Fatal("合并阶段 LLM 错误应被传播")
	}
}

// ============== AsyncTree：并发竞态与正确性 ==============

func TestAsyncTreeSummarize_ConcurrentRaceSafe(t *testing.T) {
	// 用计数 mock 验证并发下 totalTokens 累加无竞态（需配合 -race 运行）
	var callCount int64
	var mu sync.Mutex
	llmMock := mock.NewLLMProvider("counter").WithResponseFn(
		func(req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			return &llm.CompletionResponse{Content: "sum", Usage: llm.Usage{TotalTokens: 10}}, nil
		})
	s := NewAsyncTreeSummarizeSynthesizer(
		WithAsyncTreeLLM(llmMock),
		WithAsyncTreeChunkSize(2),
		WithAsyncTreeConcurrency(4),
	)
	docs := makeDocs("1", "2", "3", "4", "5", "6", "7", "8")
	resp, err := s.Synthesize(context.Background(), "q", docs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	tt, ok := resp.Metadata["total_tokens"].(int64)
	if !ok {
		t.Fatalf("total_tokens 类型应为 int64，实际 %T", resp.Metadata["total_tokens"])
	}
	// 每次调用 10 token，总调用次数 * 10 应等于 total_tokens
	if int64(callCount)*10 != tt {
		t.Errorf("token 累加竞态: callCount*10=%d, total_tokens=%d", callCount*10, tt)
	}
}

func TestAsyncTreeSummarize_OrderPreserved(t *testing.T) {
	// 验证并行合并结果按 chunk 顺序写回 results（结果数组按索引写，应保持顺序）
	llmMock := mock.NewLLMProvider("order").WithResponseFn(
		func(req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			// 回显第一个内容字符，用于观察顺序
			c := req.Messages[0].Content
			return &llm.CompletionResponse{Content: c, Usage: llm.Usage{TotalTokens: 1}}, nil
		})
	s := NewAsyncTreeSummarizeSynthesizer(
		WithAsyncTreeLLM(llmMock),
		WithAsyncTreeChunkSize(1), // chunkSize<2 应被矫正为 2
		WithAsyncTreeConcurrency(8),
	)
	docs := makeDocs("1", "2")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Synthesize(ctx, "q", docs)
		done <- err
	}()
	select {
	case err := <-done:
		// chunkSize<2 必须被矫正为 2，合并收敛、正常返回，不得触发 ctx 超时。
		// 回归: W3-43 — chunkSize<2 矫正为 2，合并循环每轮检查 ctx.Done()。
		if err != nil {
			t.Errorf("chunkSize=1 应被矫正为 2 并正常收敛，实际返回错误: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("AsyncTreeSummarize chunkSize=1 未收敛（无最小值守卫），合并循环疑似死循环")
	}
}

// TestAsyncTreeSummarize_ChunkSizeGuardMinimum 验证 chunkSize<2 被矫正为 2，
// 与 TreeSummarizeSynthesizer 行为一致。
// 回归: W3-43 — chunkSize<2 矫正为 2。
func TestAsyncTreeSummarize_ChunkSizeGuardMinimum(t *testing.T) {
	s := NewAsyncTreeSummarizeSynthesizer(WithAsyncTreeChunkSize(1))
	if s.chunkSize != 2 {
		t.Errorf("chunkSize=1 应被矫正为 2，实际 %d", s.chunkSize)
	}
	s2 := NewAsyncTreeSummarizeSynthesizer(WithAsyncTreeChunkSize(-3))
	if s2.chunkSize != 2 {
		t.Errorf("负数 chunkSize 应被矫正为 2，实际 %d", s2.chunkSize)
	}
}

// TestAsyncTreeSummarize_CtxCancelInMergeLoop 验证合并循环每轮检查 ctx.Done()，
// 上下文取消后能及时返回错误，而非依赖 LLM 自行感知取消。
// 使用一个忽略 ctx 的 LLM，确保是合并循环本身的 ctx 守卫在起作用。
// 回归: W3-43 — 合并循环每轮检查 ctx.Done()。
func TestAsyncTreeSummarize_CtxCancelInMergeLoop(t *testing.T) {
	// 忽略 ctx 的 LLM：即使 ctx 已取消也照常返回，迫使合并循环自身把关
	ignoreCtxLLM := mock.NewLLMProvider("ignore-ctx").WithResponseFn(
		func(req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			return &llm.CompletionResponse{Content: "sum", Usage: llm.Usage{TotalTokens: 1}}, nil
		})
	s := NewAsyncTreeSummarizeSynthesizer(
		WithAsyncTreeLLM(ignoreCtxLLM),
		WithAsyncTreeChunkSize(2),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 进入合成前即取消
	_, err := s.Synthesize(ctx, "q", makeDocs("1", "2", "3", "4"))
	if err == nil {
		t.Fatal("ctx 已取消，合并循环应自身检测到并返回错误")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("错误应可识别为 context.Canceled，实际: %v", err)
	}
}

func TestAsyncTreeSummarize_ErrorPropagation(t *testing.T) {
	llmMock := mock.ErrorProvider(errors.New("async-fail"))
	s := NewAsyncTreeSummarizeSynthesizer(
		WithAsyncTreeLLM(llmMock),
		WithAsyncTreeChunkSize(2),
	)
	_, err := s.Synthesize(context.Background(), "q", makeDocs("1", "2", "3"))
	if err == nil {
		t.Fatal("并行 chunk 失败应被传播")
	}
}

// ============== Accumulate：去重与编号 ==============

func TestAccumulate_DedupAndNumbering(t *testing.T) {
	// 3 个文档全返回相同答案，dedup=true 应只保留 1 个
	llmMock := mock.FixedProvider("same answer")
	s := NewAccumulateSynthesizer(
		WithAccumulateLLM(llmMock),
		WithAccumulateDedup(true),
	)
	resp, err := s.Synthesize(context.Background(), "q", makeDocs("d1", "d2", "d3"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if ac, _ := resp.Metadata["answer_count"].(int); ac != 1 {
		t.Errorf("dedup 后应只剩 1 个答案，实际 %v", resp.Metadata["answer_count"])
	}
}

func TestAccumulate_MaxAnswersLimit(t *testing.T) {
	llmMock := mock.SequenceProvider("a1", "a2", "a3", "a4", "a5")
	s := NewAccumulateSynthesizer(
		WithAccumulateLLM(llmMock),
		WithAccumulateMaxAnswers(2),
		WithAccumulateDedup(false),
	)
	resp, err := s.Synthesize(context.Background(), "q", makeDocs("1", "2", "3", "4", "5"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if dc, _ := resp.Metadata["doc_count"].(int); dc != 2 {
		t.Errorf("maxAnswers=2 应只处理 2 个文档，doc_count=%v", resp.Metadata["doc_count"])
	}
	if llmMock.CallCount() != 2 {
		t.Errorf("应只调用 2 次 LLM，实际 %d", llmMock.CallCount())
	}
}

func TestAccumulate_SkipsFailedDoc(t *testing.T) {
	// 第二个文档失败应被跳过而非中断整个流程
	idx := 0
	llmMock := mock.NewLLMProvider("partial").WithResponseFn(
		func(req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			idx++
			if idx == 2 {
				return nil, errors.New("transient")
			}
			return &llm.CompletionResponse{Content: "ok", Usage: llm.Usage{TotalTokens: 5}}, nil
		})
	s := NewAccumulateSynthesizer(WithAccumulateLLM(llmMock), WithAccumulateDedup(false))
	resp, err := s.Synthesize(context.Background(), "q", makeDocs("1", "2", "3"))
	if err != nil {
		t.Fatalf("单文档失败不应中断: %v", err)
	}
	if ac, _ := resp.Metadata["answer_count"].(int); ac != 2 {
		t.Errorf("3 文档跳过 1 个失败，应剩 2 个答案，实际 %v", resp.Metadata["answer_count"])
	}
}

// ============== CompactAndRefine：策略选择 + nil metadata 防护 ==============

func TestCompactAndRefine_NilLLMMetadataSafe(t *testing.T) {
	// 不配置 LLM：内部 Compact 返回占位响应，actual_strategy 应被写入，
	// 且 strategy 最终被覆盖为 compact_and_refine —— 验证不 panic
	s := NewCompactAndRefineSynthesizer(WithCompactAndRefineMaxContext(10000))
	resp, err := s.Synthesize(context.Background(), "q", makeDocs("short"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if resp.Metadata["strategy"] != "compact_and_refine" {
		t.Errorf("strategy 应为 compact_and_refine，实际 %v", resp.Metadata["strategy"])
	}
	if resp.Metadata["actual_strategy"] != "compact" {
		t.Errorf("小文档应走 compact，实际 %v", resp.Metadata["actual_strategy"])
	}
}

func TestCompactAndRefine_EmptyDocsNoActualStrategyPanic(t *testing.T) {
	// 空文档：内部不调用 compact/refine，actual_strategy 不会被设置；
	// 但末尾 response.Metadata["strategy"]= 直接写入，验证空文档分支不 panic
	s := NewCompactAndRefineSynthesizer(WithCompactAndRefineLLM(mock.FixedProvider("x")))
	resp, err := s.Synthesize(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("空文档不应出错: %v", err)
	}
	// 空文档分支直接 return，不经过末尾 strategy 覆盖，strategy 应为 compact_and_refine（来自空分支）
	if resp.Metadata["strategy"] != "compact_and_refine" {
		t.Errorf("空文档 strategy=%v", resp.Metadata["strategy"])
	}
}

// ============== Generation / NoText / CustomPrompt ==============

func TestGeneration_IgnoresDocs(t *testing.T) {
	llmMock := mock.EchoProvider()
	s := NewGenerationSynthesizer(WithGenerationLLM(llmMock))
	resp, err := s.Synthesize(context.Background(), "纯问题", makeDocs("无关文档"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	// Generation 直接把 query 作为 user 消息，不含文档
	if strings.Contains(resp.Content, "无关文档") {
		t.Errorf("Generation 不应注入文档内容: %q", resp.Content)
	}
	if resp.Content != "Echo: 纯问题" {
		t.Errorf("应回显纯问题，实际 %q", resp.Content)
	}
	// Generation 不设置 SourceDocuments
	if len(resp.SourceDocuments) != 0 {
		t.Errorf("Generation 不应包含源文档，实际 %d", len(resp.SourceDocuments))
	}
}

func TestNoText_AlwaysEmptyContent(t *testing.T) {
	s := NewNoTextSynthesizer()
	docs := makeDocs("a", "b")
	resp, err := s.Synthesize(context.Background(), "q", docs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("NoText 内容应为空，实际 %q", resp.Content)
	}
	if len(resp.SourceDocuments) != 2 {
		t.Errorf("应返回 2 个源文档，实际 %d", len(resp.SourceDocuments))
	}
	if dc, _ := resp.Metadata["doc_count"].(int); dc != 2 {
		t.Errorf("doc_count=%v", resp.Metadata["doc_count"])
	}
}

func TestNoText_EmptyDocs(t *testing.T) {
	s := NewNoTextSynthesizer()
	resp, err := s.Synthesize(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if dc, _ := resp.Metadata["doc_count"].(int); dc != 0 {
		t.Errorf("空文档 doc_count 应为 0，实际 %v", resp.Metadata["doc_count"])
	}
}

func TestCustomPrompt_PlaceholderReplacement(t *testing.T) {
	llmMock := mock.EchoProvider()
	s := NewCustomPromptSynthesizer(
		WithCustomPromptLLM(llmMock),
		WithCustomPromptTemplate("Q={query} C={context} N={doc_count}"),
	)
	resp, err := s.Synthesize(context.Background(), "myquery", makeDocs("c1", "c2"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	// 占位符均应被替换
	for _, ph := range []string{"{query}", "{context}", "{doc_count}"} {
		if strings.Contains(resp.Content, ph) {
			t.Errorf("占位符 %s 未替换: %q", ph, resp.Content)
		}
	}
	if !strings.Contains(resp.Content, "N=2") {
		t.Errorf("doc_count 应替换为 2: %q", resp.Content)
	}
}

func TestCustomPrompt_NoLLMReturnsRenderedPrompt(t *testing.T) {
	s := NewCustomPromptSynthesizer(
		WithCustomPromptTemplate("[{query}]||[{context}]"),
	)
	resp, err := s.Synthesize(context.Background(), "QQ", makeDocs("CC"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if resp.Content != "[QQ]||[CC]" {
		t.Errorf("无 LLM 应返回渲染后的 prompt，实际 %q", resp.Content)
	}
}

func TestCustomPrompt_SystemPromptInjected(t *testing.T) {
	llmMock := mock.FixedProvider("x")
	s := NewCustomPromptSynthesizer(
		WithCustomPromptLLM(llmMock),
		WithCustomSystemPrompt("你是专家"),
	)
	_, err := s.Synthesize(context.Background(), "q", makeDocs("d"))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	msgs := llmMock.Calls()[0].Messages
	if len(msgs) != 2 || msgs[0].Role != llm.RoleSystem || msgs[0].Content != "你是专家" {
		t.Errorf("系统提示词未正确注入: %+v", msgs)
	}
}

// ============== Pipeline：错误路径与正常路径 ==============

func TestPipeline_NoRetrieverError(t *testing.T) {
	s := NewPipelineSynthesizer(WithPipelineLLM(mock.FixedProvider("x")))
	_, err := s.RetrieveAndSynthesize(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "检索器未配置") {
		t.Errorf("无检索器应返回'检索器未配置'，实际 %v", err)
	}
}

func TestPipeline_NoLLMError(t *testing.T) {
	r := mock.FixedRetriever(makeDocs("a"))
	s := NewPipelineSynthesizer(WithPipelineRetriever(r))
	_, err := s.RetrieveAndSynthesize(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "LLM 未配置") {
		t.Errorf("无 LLM 应返回'LLM 未配置'，实际 %v", err)
	}
}

func TestPipeline_RetrieveAndSynthesize_Success(t *testing.T) {
	r := mock.FixedRetriever(makeDocs("doc1", "doc2", "doc3"))
	llmMock := mock.FixedProvider("pipeline-answer")
	s := NewPipelineSynthesizer(
		WithPipelineLLM(llmMock),
		WithPipelineRetriever(r),
		WithPipelineBufferSize(2),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := s.RetrieveAndSynthesize(ctx, "q")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	var chunks []StreamChunk
	for c := range out {
		chunks = append(chunks, c)
	}
	if len(chunks) != 1 {
		t.Fatalf("期望 1 个最终 chunk，实际 %d", len(chunks))
	}
	if chunks[0].Text != "pipeline-answer" {
		t.Errorf("内容应为 pipeline-answer，实际 %q", chunks[0].Text)
	}
}

func TestPipeline_RetrieverError_ClosesOutput(t *testing.T) {
	// 检索器报错时，docChan 关闭、firstBatch 仍为 true → firstBatchDone 关闭，
	// 但 allDocs 为空，会用空文档去 Synthesize。验证不挂死、通道正常关闭。
	r := mock.ErrorRetriever(errors.New("retrieve-fail"))
	llmMock := mock.FixedProvider("empty-answer")
	s := NewPipelineSynthesizer(
		WithPipelineLLM(llmMock),
		WithPipelineRetriever(r),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := s.RetrieveAndSynthesize(ctx, "q")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	done := make(chan struct{})
	var n int
	go func() {
		for range out {
			n++
		}
		close(done)
	}()
	select {
	case <-done:
		t.Logf("检索器报错后输出通道正常关闭，发出 %d 个 chunk（空文档仍触发了合成）", n)
	case <-time.After(4 * time.Second):
		t.Fatal("检索器报错后输出通道未关闭，疑似挂死")
	}
}

// ============== Factory ==============

func TestNewExtended_AllTypes(t *testing.T) {
	tests := []struct {
		typ  SynthesizerType
		name string
	}{
		{TypeAccumulate, "accumulate_synthesizer"},
		{TypeGeneration, "generation_synthesizer"},
		{TypeNoText, "no_text_synthesizer"},
		{TypeCompactAndRefine, "compact_and_refine_synthesizer"},
		{TypeAsyncTreeSummarize, "async_tree_summarize_synthesizer"},
		{TypeCustomPrompt, "custom_prompt_synthesizer"},
		{TypeRefine, "refine_synthesizer"},     // 回退到 New
		{"bogus", "compact_synthesizer"},       // 回退到 New 默认
	}
	for _, tc := range tests {
		s := NewExtended(tc.typ)
		if s.Name() != tc.name {
			t.Errorf("NewExtended(%s): 期望 %s，实际 %s", tc.typ, tc.name, s.Name())
		}
	}
}

func TestAvailableStrategies_Count(t *testing.T) {
	got := AvailableStrategies()
	if len(got) != 10 {
		t.Errorf("期望 10 种策略，实际 %d", len(got))
	}
}

// ============== Refine/Tree/Async 占位响应一致性（无 LLM）==============

func TestNoLLM_PlaceholderConsistency(t *testing.T) {
	docs := makeDocs("d1", "d2")
	cases := []struct {
		name     string
		s        Synthesizer
		strategy string
	}{
		{"refine", NewRefineSynthesizer(), "refine"},
		{"compact", NewCompactSynthesizer(), "compact"},
		{"tree", NewTreeSummarizeSynthesizer(), "tree_summarize"},
		{"simple", NewSimpleSummarizeSynthesizer(), "simple_summarize"},
		{"async_tree", NewAsyncTreeSummarizeSynthesizer(), "async_tree_summarize"},
		{"accumulate", NewAccumulateSynthesizer(), "accumulate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := tc.s.Synthesize(context.Background(), "q", docs)
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if resp.Metadata["strategy"] != tc.strategy {
				t.Errorf("strategy 应为 %s，实际 %v", tc.strategy, resp.Metadata["strategy"])
			}
			if len(resp.SourceDocuments) != 2 {
				t.Errorf("无 LLM 占位响应应含 2 源文档，实际 %d", len(resp.SourceDocuments))
			}
		})
	}
}

// TestIncludeSourceFalse 验证 WithSourceDocuments(false) 时不返回源文档
func TestIncludeSourceFalse(t *testing.T) {
	llmMock := mock.FixedProvider("ans")
	s := NewCompactSynthesizer(WithCompactSynthesizerLLM(llmMock))
	resp, err := s.Synthesize(context.Background(), "q", makeDocs("d1"),
		WithSourceDocuments(false))
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if len(resp.SourceDocuments) != 0 {
		t.Errorf("includeSource=false 时不应返回源文档，实际 %d", len(resp.SourceDocuments))
	}
}
