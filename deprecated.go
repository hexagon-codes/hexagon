// Deprecated: 以下为过渡性重导出，请直接 import 对应子包。
// 本文件将在下一个大版本中移除。
package hexagon

import (
	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/llm/openai"
	"github.com/hexagon-codes/ai-core/memory"
	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/ai-core/store/vector/qdrant"
	"github.com/hexagon-codes/hexagon/agent"
	"github.com/hexagon-codes/hexagon/agent/skill"
	"github.com/hexagon-codes/hexagon/llm/conversation"
	"github.com/hexagon-codes/hexagon/mcp"
	memstore "github.com/hexagon-codes/hexagon/memory/store"
	"github.com/hexagon-codes/hexagon/observe/eventstream"
	"github.com/hexagon-codes/hexagon/observe/metrics"
	"github.com/hexagon-codes/hexagon/observe/tracer"
	"github.com/hexagon-codes/hexagon/orchestration/chain"
	"github.com/hexagon-codes/hexagon/orchestration/graph"
	"github.com/hexagon-codes/hexagon/plugin"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/hexagon/rag/embedder"
	"github.com/hexagon-codes/hexagon/rag/indexer"
	"github.com/hexagon-codes/hexagon/rag/loader"
	"github.com/hexagon-codes/hexagon/rag/retriever"
	"github.com/hexagon-codes/hexagon/rag/splitter"
	"github.com/hexagon-codes/hexagon/security/cost"
	"github.com/hexagon-codes/hexagon/security/guard"
)

// ============== 重新导出子包便捷函数 ==============

// NewReActAgent 创建 ReAct Agent
var NewReActAgent = agent.NewReAct

// NewBufferMemory 创建缓冲记忆
var NewBufferMemory = memory.NewBuffer

// NewOpenAI 创建 OpenAI Provider
var NewOpenAI = openai.New

// OpenAI Provider 配置选项
var (
	// OpenAIWithBaseURL 设置自定义 API 端点（支持中转/私有部署）
	OpenAIWithBaseURL = openai.WithBaseURL

	// OpenAIWithModel 设置默认模型
	OpenAIWithModel = openai.WithModel

	// OpenAIWithHTTPClient 设置自定义 HTTP 客户端
	OpenAIWithHTTPClient = openai.WithHTTPClient

	// OpenAIEmbeddingDimension 获取 OpenAI Embedding 模型的默认维度
	OpenAIEmbeddingDimension = openai.EmbeddingDimension
)

// ============== 编排引擎 ==============

// NewGraph 创建图编排构建器
//
// 示例：
//
//	g, _ := hexagon.NewGraph[MyState]("my-graph").
//	    AddNode("step1", handler1).
//	    AddNode("step2", handler2).
//	    AddEdge(hexagon.START, "step1").
//	    AddEdge("step1", "step2").
//	    AddEdge("step2", hexagon.END).
//	    Build()
func NewGraph[S graph.State](name string) *graph.GraphBuilder[S] {
	return graph.NewGraph[S](name)
}

// NewChain 创建链式编排构建器
//
// 示例：
//
//	c, _ := hexagon.NewChain[Input, Output]("my-chain").
//	    Pipe(step1).
//	    Pipe(step2).
//	    Build()
func NewChain[I, O any](name string) *chain.ChainBuilder[I, O] {
	return chain.NewChain[I, O](name)
}

// 图编排常量
const (
	// START 起始节点
	START = graph.START
	// END 结束节点
	END = graph.END
)

// ============== 多 Agent 协作 ==============

// NewTeam 创建 Agent 团队
//
// 示例：
//
//	team := hexagon.NewTeam("research-team",
//	    hexagon.WithAgents(researcher, writer),
//	    hexagon.WithMode(hexagon.TeamModeSequential),
//	)
var NewTeam = agent.NewTeam

// TransferTo 创建 Agent 交接工具
//
// 示例：
//
//	tools := []hexagon.Tool{
//	    hexagon.TransferTo(salesAgent),
//	    hexagon.TransferTo(supportAgent),
//	}
var TransferTo = agent.TransferTo

// 团队模式常量
const (
	TeamModeSequential    = agent.TeamModeSequential
	TeamModeHierarchical  = agent.TeamModeHierarchical
	TeamModeCollaborative = agent.TeamModeCollaborative
	TeamModeRoundRobin    = agent.TeamModeRoundRobin
)

// 团队选项
var (
	WithAgents          = agent.WithAgents
	WithMode            = agent.WithMode
	WithManager         = agent.WithManager
	WithMaxRounds       = agent.WithMaxRounds
	WithTeamDescription = agent.WithTeamDescription
)

// ============== 可观测性 ==============

// NewTracer 创建内存追踪器
//
// 示例：
//
//	tracer := hexagon.NewTracer()
//	ctx := hexagon.ContextWithTracer(ctx, tracer)
var NewTracer = tracer.NewMemoryTracer

// NewNoopTracer 创建空追踪器（禁用追踪）
var NewNoopTracer = tracer.NewNoopTracer

// ContextWithTracer 将追踪器添加到 context
var ContextWithTracer = tracer.ContextWithTracer

// StartSpan 开始新的追踪 Span
var StartSpan = tracer.StartSpan

// NewMetrics 创建内存指标收集器
//
// 示例：
//
//	m := hexagon.NewMetrics()
//	m.Counter("agent_calls", "agent", "react").Inc()
var NewMetrics = metrics.NewMemoryMetrics

// ============== 安全防护 ==============

// NewPromptInjectionGuard 创建 Prompt 注入检测守卫
//
// 示例：
//
//	guard := hexagon.NewPromptInjectionGuard()
//	result, _ := guard.Check(ctx, userInput)
//	if !result.Passed {
//	    // 处理潜在的注入攻击
//	}
var NewPromptInjectionGuard = guard.NewPromptInjectionGuard

// NewPIIGuard 创建 PII 检测守卫
var NewPIIGuard = guard.NewPIIGuard

// NewGuardChain 创建守卫链
var NewGuardChain = guard.NewGuardChain

// 守卫链模式
const (
	ChainModeAll   = guard.ChainModeAll
	ChainModeAny   = guard.ChainModeAny
	ChainModeFirst = guard.ChainModeFirst
)

// NewCostController 创建成本控制器
//
// 示例：
//
//	controller := hexagon.NewCostController(
//	    hexagon.WithBudget(10.0),  // $10 预算
//	    hexagon.WithMaxTokensTotal(100000),
//	)
var NewCostController = cost.NewController

// 成本控制选项
var (
	WithBudget              = cost.WithBudget
	WithMaxTokensPerRequest = cost.WithMaxTokensPerRequest
	WithMaxTokensPerSession = cost.WithMaxTokensPerSession
	WithMaxTokensTotal      = cost.WithMaxTokensTotal
	WithRequestsPerMinute   = cost.WithRequestsPerMinute
)

// ============== 状态管理 ==============

// NewStateManager 创建状态管理器
//
// 示例：
//
//	sm := hexagon.NewStateManager("session-123", nil)
//	sm.Turn().Set("key", "value")
//	sm.Session().Set("user_id", 123)
var NewStateManager = agent.NewStateManager

// NewGlobalState 创建全局状态
var NewGlobalState = agent.NewGlobalState

// Agent Option 便捷导出
var (
	// AgentWithLLM 设置 Agent 的 LLM Provider
	AgentWithLLM = agent.WithLLM

	// AgentWithTools 设置 Agent 的工具列表
	AgentWithTools = agent.WithTools

	// AgentWithSystemPrompt 设置 Agent 的系统提示词
	AgentWithSystemPrompt = agent.WithSystemPrompt

	// AgentWithMaxIterations 设置 Agent 的最大迭代次数
	AgentWithMaxIterations = agent.WithMaxIterations

	// AgentWithMemory 设置 Agent 的记忆系统
	AgentWithMemory = agent.WithMemory

	// AgentWithName 设置 Agent 名称
	AgentWithName = agent.WithName

	// AgentWithVerbose 设置 Agent 详细输出模式
	AgentWithVerbose = agent.WithVerbose

	// AgentWithRole 设置 Agent 角色
	AgentWithRole = agent.WithRole

	// AgentWithID 设置 Agent ID
	AgentWithID = agent.WithID

	// AgentWithDescription 设置 Agent 描述
	AgentWithDescription = agent.WithDescription
)

// NewRole 创建角色构建器
//
// 示例：
//
//	role := hexagon.NewRole("researcher").
//	    Title("高级研究员").
//	    Goal("深入研究和分析问题").
//	    Build()
var NewRole = agent.NewRole

// RoleBuilder 是角色构建器类型
type RoleBuilder = agent.RoleBuilder

// ============== 类型重新导出 ==============

// LLM 相关类型
type (
	// CompletionRequest 是 LLM 补全请求
	CompletionRequest = llm.CompletionRequest

	// CompletionResponse 是 LLM 补全响应
	CompletionResponse = llm.CompletionResponse

	// Usage 是 Token 使用统计
	Usage = llm.Usage

	// LLMRole 是消息角色类型
	LLMRole = llm.Role

	// OpenAIOption 是 OpenAI Provider 的配置选项
	OpenAIOption = openai.Option

	// LLMStream 是 LLM 流式响应
	LLMStream = llm.Stream

	// LLMStreamChunk 是流式响应的单个片段
	LLMStreamChunk = llm.StreamChunk

	// LLMStreamResult 是流式响应的最终结果
	LLMStreamResult = llm.StreamResult
)

// LLM 角色常量
const (
	RoleSystem    = llm.RoleSystem
	RoleUser      = llm.RoleUser
	RoleAssistant = llm.RoleAssistant
	RoleTool      = llm.RoleTool
)

// Agent 相关类型
type (
	// AgentOption 是 Agent 配置选项
	AgentOption = agent.Option

	// Role 是角色定义
	Role = agent.Role

	// Team 是团队
	Team = agent.Team

	// StateManager 是状态管理器接口
	StateManager = agent.StateManager
)

// 图编排相关类型
type (
	// Graph 是编译后的图
	Graph[S graph.State] = graph.Graph[S]

	// Chain 是链式组件
	Chain[I, O any] = chain.Chain[I, O]

	// State 是图状态接口
	State = graph.State

	// MapState 是通用 map 状态
	MapState = graph.MapState

	// NodeHandler 节点处理函数类型
	NodeHandler[S graph.State] = graph.NodeHandler[S]

	// StateMerger 状态合并函数类型
	StateMerger[S graph.State] = graph.StateMerger[S]
)

// ParallelNodeWithMerger 创建带自定义状态合并器的并行执行节点
//
// 示例：
//
//	node := hexagon.ParallelNodeWithMerger[MyState]("parallel", merger, handler1, handler2)
func ParallelNodeWithMerger[S graph.State](name string, merger graph.StateMerger[S], handlers ...graph.NodeHandler[S]) *graph.Node[S] {
	return graph.ParallelNodeWithMerger(name, merger, handlers...)
}

// 可观测性相关类型
type (
	// Tracer 是追踪器接口
	Tracer = tracer.Tracer

	// Span 是追踪 Span 接口
	Span = tracer.Span

	// Metrics 是指标接口
	Metrics = metrics.Metrics
)

// 安全相关类型
type (
	// Guard 是守卫接口
	Guard = guard.Guard

	// CheckResult 是守卫检查结果
	CheckResult = guard.CheckResult

	// GuardFinding 是守卫发现的问题
	GuardFinding = guard.Finding

	// GuardPosition 是问题在文本中的位置
	GuardPosition = guard.Position

	// PromptInjectionGuard 是 Prompt 注入检测守卫
	PromptInjectionGuard = guard.PromptInjectionGuard

	// PIIGuard 是 PII 检测守卫
	PIIGuard = guard.PIIGuard

	// GuardChain 是守卫链
	GuardChain = guard.GuardChain

	// ChainMode 是守卫链模式
	ChainMode = guard.ChainMode

	// CostController 是成本控制器
	CostController = cost.Controller
)

// ============== RAG 系统 ==============

// NewRAGEngine 创建 RAG 引擎
//
// 示例：
//
//	engine := hexagon.NewRAGEngine(
//	    hexagon.WithRAGStore(vectorStore),
//	    hexagon.WithRAGEmbedder(embedder),
//	)
//	docs, _ := engine.Retrieve(ctx, "What is Go?")
var NewRAGEngine = rag.NewEngine

// RAG 引擎选项
var (
	WithRAGStore    = rag.WithStore
	WithRAGEmbedder = rag.WithEngineEmbedder
	WithRAGLoader   = rag.WithLoader
	WithRAGSplitter = rag.WithEngineSplitter
	WithRAGTopK     = rag.WithEngineTopK
	WithRAGMinScore = rag.WithEngineMinScore
)

// RAG 检索选项
var (
	// WithFilter 设置元数据过滤条件
	WithFilter = rag.WithFilter

	// WithTopK 设置返回文档数量
	WithTopK = rag.WithTopK

	// WithMinScore 设置最小相关性分数
	WithMinScore = rag.WithMinScore
)

// NewRAGPipeline 创建 RAG 管道
var NewRAGPipeline = rag.NewPipeline

// ============== RAG 组件 ==============

// 文档加载器
var (
	// NewTextLoader 创建文本文件加载器
	NewTextLoader = loader.NewTextLoader

	// NewMarkdownLoader 创建 Markdown 文件加载器
	NewMarkdownLoader = loader.NewMarkdownLoader

	// NewDirectoryLoader 创建目录批量加载器
	NewDirectoryLoader = loader.NewDirectoryLoader

	// NewURLLoader 创建 URL 加载器
	NewURLLoader = loader.NewURLLoader

	// NewStringLoader 创建字符串加载器
	NewStringLoader = loader.NewStringLoader
)

// 文档分割器
var (
	// NewCharacterSplitter 创建字符分割器
	NewCharacterSplitter = splitter.NewCharacterSplitter

	// NewRecursiveSplitter 创建递归分割器
	NewRecursiveSplitter = splitter.NewRecursiveSplitter

	// NewMarkdownSplitter 创建 Markdown 分割器
	NewMarkdownSplitter = splitter.NewMarkdownSplitter

	// NewSentenceSplitter 创建句子分割器
	NewSentenceSplitter = splitter.NewSentenceSplitter
)

// 文档检索器
var (
	// NewVectorRetriever 创建向量检索器
	NewVectorRetriever = retriever.NewVectorRetriever

	// NewKeywordRetriever 创建关键词检索器
	NewKeywordRetriever = retriever.NewKeywordRetriever

	// NewHybridRetriever 创建混合检索器
	NewHybridRetriever = retriever.NewHybridRetriever

	// NewMultiRetriever 创建多源检索器
	NewMultiRetriever = retriever.NewMultiRetriever
)

// 文档索引器
var (
	// NewVectorIndexer 创建向量索引器
	NewVectorIndexer = indexer.NewVectorIndexer

	// NewConcurrentIndexer 创建并发索引器
	NewConcurrentIndexer = indexer.NewConcurrentIndexer

	// NewIncrementalIndexer 创建增量索引器
	NewIncrementalIndexer = indexer.NewIncrementalIndexer
)

// 向量生成器
var (
	// NewOpenAIEmbedder 创建 OpenAI Embedder
	NewOpenAIEmbedder = embedder.NewOpenAIEmbedder

	// NewCachedEmbedder 创建带缓存的 Embedder
	NewCachedEmbedder = embedder.NewCachedEmbedder

	// NewMockEmbedder 创建模拟 Embedder（用于测试）
	NewMockEmbedder = embedder.NewMockEmbedder

	// Embedder 选项
	WithEmbedderModel     = embedder.WithModel
	WithEmbedderDimension = embedder.WithDimension
)

// 分割器选项
var (
	WithRecursiveChunkSize    = splitter.WithRecursiveChunkSize
	WithRecursiveChunkOverlap = splitter.WithRecursiveChunkOverlap
)

// 向量存储类型 (ai-core/store/vector 层, 不同于 rag.VectorStore)
type (
	// VectorDocument 向量存储文档
	VectorDocument = vector.Document

	// VectorEmbedder 向量生成器接口 (store 层)
	VectorEmbedder = vector.Embedder

	// VectorSearchOption 向量搜索选项
	VectorSearchOption = vector.SearchOption

	// VectorMemoryStore 内存向量存储 (store 层的具体 Store 接口)
	VectorMemoryStoreInterface = vector.Store
)

// 向量搜索选项
var (
	WithVectorMinScore = vector.WithMinScore
)

// 向量存储
var (
	// NewMemoryVectorStore 创建内存向量存储
	NewMemoryVectorStore = vector.NewMemoryStore

	// NewQdrantStore 创建 Qdrant 向量存储
	//
	// 示例：
	//
	//	store, err := hexagon.NewQdrantStore(hexagon.QdrantConfig{
	//	    Host:             "localhost",
	//	    Port:             6333,
	//	    Collection:       "documents",
	//	    Dimension:        1536,
	//	    CreateCollection: true,
	//	})
	NewQdrantStore = qdrant.New
)

// ============== MCP V2（基于官方 SDK） ==============

// ConnectMCPServer 使用官方 SDK 连接 MCP Server 并获取工具列表
//
// 返回的 []tool.Tool 可直接用于 Hexagon Agent。
// 调用方需要在使用完毕后调用 closer.Close() 释放连接。
//
// 示例：
//
//	tools, closer, err := hexagon.ConnectMCPServer(ctx, transport)
//	defer closer.Close()
//	agent := hexagon.QuickStart(hexagon.WithTools(tools...))
var ConnectMCPServer = mcp.ConnectMCPServerV2

// ConnectMCPStdio 通过 Stdio 连接 MCP Server
//
// 启动子进程并通过 stdin/stdout 通信。
//
// 示例：
//
//	tools, cleanup, err := hexagon.ConnectMCPStdio(ctx, "npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")
//	defer cleanup()
var ConnectMCPStdio = mcp.ConnectStdioServerV2

// ConnectMCPSSE 通过 SSE 连接 MCP Server
//
// 示例：
//
//	tools, closer, err := hexagon.ConnectMCPSSE(ctx, "http://localhost:8080/sse")
//	defer closer.Close()
var ConnectMCPSSE = mcp.ConnectSSEServerV2

// ConnectMCPStreamable 通过 Streamable HTTP 连接 MCP Server (2025-03-26 标准)
//
// 示例：
//
//	tools, closer, err := hexagon.ConnectMCPStreamable(ctx, "http://localhost:8080/mcp")
//	defer closer.Close()
var ConnectMCPStreamable = mcp.ConnectStreamableServerV2

// NewMCPServer 创建基于官方 SDK 的 MCP 服务器
//
// 将 Hexagon/ai-core 工具暴露为标准 MCP 服务。
//
// 示例：
//
//	server := hexagon.NewMCPServer("my-tools", "1.0.0")
//	server.RegisterTool(myCalculator)
//	server.ServeStdio(ctx)
var NewMCPServer = mcp.NewMCPServerV2

// MCP 相关类型
type (
	// MCPServerV2 是基于官方 SDK 的 MCP 服务器
	MCPServerV2 = mcp.ServerV2
)

// ============== 跨会话持久记忆 ==============

// NewInMemoryStore 创建内存记忆存储
//
// 纯内存实现，适合开发和测试。支持命名空间隔离、TTL、关键词搜索。
//
// 示例：
//
//	store := hexagon.NewInMemoryStore()
//	store.Put(ctx, []string{"users", "u1"}, "prefs", map[string]any{"theme": "dark"})
var NewInMemoryStore = memstore.NewInMemoryStore

// NewFileStore 创建文件持久化记忆存储
//
// 基于文件系统的持久化存储，支持原子写入和 TTL 过期。
//
// 示例：
//
//	store, err := hexagon.NewFileStore("/data/memory")
var NewFileStore = memstore.NewFileStore

// NewRedisStore 创建 Redis 持久化记忆存储
//
// 基于 Redis 的高性能持久化存储，支持命名空间隔离和 Pipeline 操作。
var NewRedisStore = memstore.NewRedisStore

// NewPersistentMemory 创建持久记忆适配器
//
// 将 MemoryStore 适配为 ai-core memory.Memory 接口，
// 使现有 Agent 无缝使用持久化存储。
//
// 示例：
//
//	store := hexagon.NewInMemoryStore()
//	mem := hexagon.NewPersistentMemory(store, []string{"users", "u123"})
//	agent := hexagon.QuickStart(hexagon.WithMemory(mem))
var NewPersistentMemory = memstore.NewPersistentMemory

// 记忆存储相关类型
type (
	// MemoryStore 是跨会话持久记忆存储接口
	MemoryStore = memstore.MemoryStore

	// MemoryItem 是记忆存储条目
	MemoryItem = memstore.Item
)

// QdrantConfig 是 Qdrant 配置
type QdrantConfig = qdrant.Config

// Qdrant 距离度量方式
const (
	QdrantDistanceCosine = qdrant.DistanceCosine
	QdrantDistanceEuclid = qdrant.DistanceEuclid
	QdrantDistanceDot    = qdrant.DistanceDot
)

// Qdrant 选项式创建
var NewQdrantStoreWithOptions = qdrant.NewWithOptions

// Qdrant 配置选项
var (
	QdrantWithHost             = qdrant.WithHost
	QdrantWithPort             = qdrant.WithPort
	QdrantWithCollection       = qdrant.WithCollection
	QdrantWithDimension        = qdrant.WithDimension
	QdrantWithAPIKey           = qdrant.WithAPIKey
	QdrantWithHTTPS            = qdrant.WithHTTPS
	QdrantWithTimeout          = qdrant.WithTimeout
	QdrantWithDistance         = qdrant.WithDistance
	QdrantWithOnDisk           = qdrant.WithOnDisk
	QdrantWithCreateCollection = qdrant.WithCreateCollection
)

// RAG 相关类型
type (
	// Document 是 RAG 文档
	Document = rag.Document

	// Loader 是文档加载器接口
	Loader = rag.Loader

	// Splitter 是文档分割器接口
	Splitter = rag.Splitter

	// Indexer 是文档索引器接口
	Indexer = rag.Indexer

	// Retriever 是文档检索器接口
	Retriever = rag.Retriever

	// Embedder 是向量生成器接口
	Embedder = rag.Embedder

	// VectorStore 是向量存储接口
	VectorStore = rag.VectorStore

	// OpenAIEmbedder OpenAI 向量生成器
	OpenAIEmbedder = embedder.OpenAIEmbedder

	// RAGEngine 是 RAG 引擎
	RAGEngine = rag.Engine
)

// ============== 对话管理器 ==============

// NewConversationManager 创建多轮对话管理器
//
// 示例：
//
//	mgr := hexagon.NewConversationManager(
//	    hexagon.ConvWithMaxTokens(4096),
//	    hexagon.ConvWithSystemPrompt("你是助手"),
//	)
var NewConversationManager = conversation.New

// 对话管理器类型
type ConversationManager = conversation.Manager

// 对话管理器选项
var (
	ConvWithMaxTokens    = conversation.WithMaxTokens
	ConvWithMaxTurns     = conversation.WithMaxTurns
	ConvWithSystemPrompt = conversation.WithSystemPrompt
)

// ============== 对话 Agent ==============

// NewConversationAgent 创建多轮对话 Agent
//
// 示例：
//
//	conv := hexagon.NewConversationAgent(myAgent,
//	    hexagon.ConvAgentMaxTurns(20),
//	)
//	output, _ := conv.Chat(ctx, "你好")
var NewConversationAgent = agent.NewConversation

// 对话 Agent 类型
type ConversationAgent = agent.ConversationAgent

// 对话 Agent 选项
var (
	ConvAgentMaxTurns  = agent.WithConvMaxTurns
	ConvAgentMaxTokens = agent.WithConvMaxTokens
)

// ============== Skill 系统 ==============

// NewSkillRegistry 创建技能注册中心
var NewSkillRegistry = skill.NewRegistry

// Skill 相关类型
type (
	// Skill 是技能定义
	Skill = skill.Skill

	// SkillRegistry 是技能注册中心
	SkillRegistry = skill.Registry
)

// Skill 签名验证
var NewHMACSigner = skill.NewHMACSigner

// ============== 插件系统 ==============

// NewPluginRegistry 创建插件注册中心
var NewPluginRegistry = plugin.NewRegistry

// NewPluginBasePlugin 创建基础插件实例
var NewPluginBasePlugin = plugin.NewBasePlugin

// 插件相关类型
type (
	// PluginPlugin 是插件接口
	PluginPlugin = plugin.Plugin

	// PluginInfo 是插件信息
	PluginInfo = plugin.PluginInfo

	// PluginType 是插件类型枚举
	PluginType = plugin.PluginType

	// PluginRegistry 是插件注册中心
	PluginRegistry = plugin.Registry

	// PluginBasePlugin 是基础插件实现
	PluginBasePlugin = plugin.BasePlugin
)

// ============== 事件流 ==============

// NewEventStream 创建 Agent 事件流
//
// 示例：
//
//	stream := hexagon.NewEventStream()
//	ch, unsub := stream.Subscribe()
//	defer unsub()
var NewEventStream = eventstream.New

// 事件流类型
type (
	// EventStream 是事件流
	EventStream = eventstream.Stream

	// AgentEvent 是 Agent 事件
	AgentEvent = eventstream.Event

	// AgentEventType 是事件类型
	AgentEventType = eventstream.EventType
)

// 事件流选项
var EventStreamBufferSize = eventstream.WithBufferSize

// 预定义事件类型常量
const (
	EventAgentStart  = eventstream.EventAgentStart
	EventAgentEnd    = eventstream.EventAgentEnd
	EventAgentError  = eventstream.EventAgentError
	EventToolCall    = eventstream.EventToolCall
	EventToolResult  = eventstream.EventToolResult
	EventLLMRequest  = eventstream.EventLLMRequest
	EventLLMResponse = eventstream.EventLLMResponse

	EventStreamStateChange = eventstream.EventStateChange
	EventStreamCheckpoint  = eventstream.EventCheckpoint
)

// ============== 指标告警 ==============

// NewAlertManager 创建指标告警管理器
var NewAlertManager = metrics.NewAlertManager

// 告警相关类型
type (
	// AlertManager 是告警管理器
	AlertManager = metrics.AlertManager

	// AlertRule 是告警规则
	AlertRule = metrics.AlertRule

	// AlertCondition 是告警条件
	AlertCondition = metrics.AlertCondition

	// Alert 是告警实例
	Alert = metrics.Alert

	// AlertSeverity 是告警严重度
	AlertSeverity = metrics.Severity
)

// 告警严重度常量
const (
	AlertSeverityInfo     = metrics.SeverityInfo
	AlertSeverityWarning  = metrics.SeverityWarning
	AlertSeverityCritical = metrics.SeverityCritical
)
