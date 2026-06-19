// Package multimodal 提供多模态 RAG 支持
//
// 本包实现多模态检索增强生成：
//   - ImageProcessor: 图像处理器
//   - AudioProcessor: 音频处理器
//   - VideoProcessor: 视频处理器
//   - MultimodalDocument: 多模态文档
//   - MultimodalRetriever: 多模态检索器
//
// 支持的内容类型：
//   - 图像: PNG, JPEG, GIF, WebP
//   - 音频: MP3, WAV, OGG
//   - 视频: MP4, WebM
//   - 文档: PDF, DOCX (带嵌入图片)
//
// 设计参考：
//   - GPT-4V, Claude Vision
package multimodal

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/internal/util"
	"github.com/hexagon-codes/hexagon/rag"
)

// ============== 内容类型定义 ==============

// ContentType 内容类型
type ContentType string

const (
	ContentTypeText  ContentType = "text"
	ContentTypeImage ContentType = "image"
	ContentTypeAudio ContentType = "audio"
	ContentTypeVideo ContentType = "video"
)

// ImageFormat 图像格式
type ImageFormat string

const (
	ImageFormatPNG  ImageFormat = "png"
	ImageFormatJPEG ImageFormat = "jpeg"
	ImageFormatGIF  ImageFormat = "gif"
	ImageFormatWebP ImageFormat = "webp"
)

// AudioFormat 音频格式
type AudioFormat string

const (
	AudioFormatMP3 AudioFormat = "mp3"
	AudioFormatWAV AudioFormat = "wav"
	AudioFormatOGG AudioFormat = "ogg"
)

// VideoFormat 视频格式
type VideoFormat string

const (
	VideoFormatMP4  VideoFormat = "mp4"
	VideoFormatWebM VideoFormat = "webm"
)

// ============== 多模态内容 ==============

// Content 多模态内容
type Content struct {
	// Type 内容类型
	Type ContentType `json:"type"`

	// Text 文本内容 (Type=text 时使用)
	Text string `json:"text,omitempty"`

	// Data 二进制数据 (图像/音频/视频)
	Data []byte `json:"data,omitempty"`

	// DataURL 数据URL (base64 编码)
	DataURL string `json:"data_url,omitempty"`

	// URL 外部URL
	URL string `json:"url,omitempty"`

	// Format 格式 (图像/音频/视频格式)
	Format string `json:"format,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// IsText 是否为文本
func (c *Content) IsText() bool {
	return c.Type == ContentTypeText
}

// IsImage 是否为图像
func (c *Content) IsImage() bool {
	return c.Type == ContentTypeImage
}

// IsAudio 是否为音频
func (c *Content) IsAudio() bool {
	return c.Type == ContentTypeAudio
}

// IsVideo 是否为视频
func (c *Content) IsVideo() bool {
	return c.Type == ContentTypeVideo
}

// ToBase64 将数据转换为 base64
func (c *Content) ToBase64() string {
	if c.DataURL != "" {
		return c.DataURL
	}
	if len(c.Data) > 0 {
		mimeType := c.getMimeType()
		return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(c.Data))
	}
	return ""
}

// getMimeType 获取 MIME 类型
func (c *Content) getMimeType() string {
	switch c.Type {
	case ContentTypeImage:
		switch ImageFormat(c.Format) {
		case ImageFormatPNG:
			return "image/png"
		case ImageFormatJPEG:
			return "image/jpeg"
		case ImageFormatGIF:
			return "image/gif"
		case ImageFormatWebP:
			return "image/webp"
		default:
			return "image/png"
		}
	case ContentTypeAudio:
		switch AudioFormat(c.Format) {
		case AudioFormatMP3:
			return "audio/mpeg"
		case AudioFormatWAV:
			return "audio/wav"
		case AudioFormatOGG:
			return "audio/ogg"
		default:
			return "audio/mpeg"
		}
	case ContentTypeVideo:
		switch VideoFormat(c.Format) {
		case VideoFormatMP4:
			return "video/mp4"
		case VideoFormatWebM:
			return "video/webm"
		default:
			return "video/mp4"
		}
	default:
		return "text/plain"
	}
}

// NewTextContent 创建文本内容
func NewTextContent(text string) *Content {
	return &Content{
		Type: ContentTypeText,
		Text: text,
	}
}

// NewImageContent 从数据创建图像内容
func NewImageContent(data []byte, format ImageFormat) *Content {
	return &Content{
		Type:   ContentTypeImage,
		Data:   data,
		Format: string(format),
	}
}

// NewImageContentFromURL 从URL创建图像内容
func NewImageContentFromURL(url string) *Content {
	return &Content{
		Type: ContentTypeImage,
		URL:  url,
	}
}

// NewImageContentFromFile 从文件创建图像内容
func NewImageContentFromFile(path string) (*Content, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read image file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	var format ImageFormat
	switch ext {
	case ".png":
		format = ImageFormatPNG
	case ".jpg", ".jpeg":
		format = ImageFormatJPEG
	case ".gif":
		format = ImageFormatGIF
	case ".webp":
		format = ImageFormatWebP
	default:
		format = ImageFormatPNG
	}

	return NewImageContent(data, format), nil
}

// NewAudioContent 从数据创建音频内容
func NewAudioContent(data []byte, format AudioFormat) *Content {
	return &Content{
		Type:   ContentTypeAudio,
		Data:   data,
		Format: string(format),
	}
}

// NewVideoContent 从数据创建视频内容
func NewVideoContent(data []byte, format VideoFormat) *Content {
	return &Content{
		Type:   ContentTypeVideo,
		Data:   data,
		Format: string(format),
	}
}

// ============== 多模态文档 ==============

// MultimodalDocument 多模态文档
type MultimodalDocument struct {
	// ID 文档唯一标识
	ID string `json:"id"`

	// Contents 内容列表 (可包含多种模态)
	Contents []*Content `json:"contents"`

	// Metadata 文档元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// Embeddings 各模态的向量表示
	Embeddings map[ContentType][]float32 `json:"embeddings,omitempty"`

	// TextDescription 文本描述 (用于存储图像描述等)
	TextDescription string `json:"text_description,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
}

// NewMultimodalDocument 创建多模态文档
func NewMultimodalDocument(contents ...*Content) *MultimodalDocument {
	return &MultimodalDocument{
		ID:         util.GenerateID("mmdoc"),
		Contents:   contents,
		Embeddings: make(map[ContentType][]float32),
		CreatedAt:  time.Now(),
	}
}

// AddContent 添加内容
func (d *MultimodalDocument) AddContent(content *Content) {
	d.Contents = append(d.Contents, content)
}

// GetText 获取所有文本内容
func (d *MultimodalDocument) GetText() string {
	var texts []string
	for _, c := range d.Contents {
		if c.IsText() {
			texts = append(texts, c.Text)
		}
	}
	if d.TextDescription != "" {
		texts = append(texts, d.TextDescription)
	}
	return strings.Join(texts, "\n")
}

// GetImages 获取所有图像内容
func (d *MultimodalDocument) GetImages() []*Content {
	var images []*Content
	for _, c := range d.Contents {
		if c.IsImage() {
			images = append(images, c)
		}
	}
	return images
}

// HasImages 是否包含图像
func (d *MultimodalDocument) HasImages() bool {
	for _, c := range d.Contents {
		if c.IsImage() {
			return true
		}
	}
	return false
}

// HasAudio 是否包含音频
func (d *MultimodalDocument) HasAudio() bool {
	for _, c := range d.Contents {
		if c.IsAudio() {
			return true
		}
	}
	return false
}

// HasVideo 是否包含视频
func (d *MultimodalDocument) HasVideo() bool {
	for _, c := range d.Contents {
		if c.IsVideo() {
			return true
		}
	}
	return false
}

// ToRAGDocument 转换为 RAG 文档
func (d *MultimodalDocument) ToRAGDocument() rag.Document {
	return rag.Document{
		ID:       d.ID,
		Content:  d.GetText(),
		Metadata: d.Metadata,
	}
}

// ============== 内容处理器接口 ==============

// ContentProcessor 内容处理器接口
type ContentProcessor interface {
	// Process 处理内容
	Process(ctx context.Context, content *Content) (*ProcessResult, error)

	// SupportedTypes 支持的内容类型
	SupportedTypes() []ContentType
}

// ProcessResult 处理结果
type ProcessResult struct {
	// TextDescription 文本描述
	TextDescription string

	// Embedding 向量表示
	Embedding []float32

	// Metadata 提取的元数据
	Metadata map[string]any
}

// ============== ImageProcessor 图像处理器 ==============

// ImageProcessor 图像处理器
type ImageProcessor struct {
	provider      llm.Provider
	model         string
	embedder      ImageEmbedder
	defaultPrompt string
}

// ImageEmbedder 图像向量化接口
type ImageEmbedder interface {
	// EmbedImage 将图像转换为向量
	EmbedImage(ctx context.Context, image *Content) ([]float32, error)

	// EmbedImages 批量向量化
	EmbedImages(ctx context.Context, images []*Content) ([][]float32, error)
}

// ImageProcessorOption ImageProcessor 选项
type ImageProcessorOption func(*ImageProcessor)

// WithImageModel 设置图像理解模型
func WithImageModel(model string) ImageProcessorOption {
	return func(p *ImageProcessor) {
		p.model = model
	}
}

// WithImageEmbedder 设置图像向量化器
func WithImageEmbedder(embedder ImageEmbedder) ImageProcessorOption {
	return func(p *ImageProcessor) {
		p.embedder = embedder
	}
}

// WithImagePrompt 设置图像描述提示词
func WithImagePrompt(prompt string) ImageProcessorOption {
	return func(p *ImageProcessor) {
		p.defaultPrompt = prompt
	}
}

// NewImageProcessor 创建图像处理器
func NewImageProcessor(provider llm.Provider, opts ...ImageProcessorOption) *ImageProcessor {
	p := &ImageProcessor{
		provider:      provider,
		defaultPrompt: "请详细描述这张图片的内容，包括主要对象、场景、文字、颜色等信息。",
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Process 处理图像
func (p *ImageProcessor) Process(ctx context.Context, content *Content) (*ProcessResult, error) {
	if !content.IsImage() {
		return nil, fmt.Errorf("content is not an image")
	}

	result := &ProcessResult{
		Metadata: make(map[string]any),
	}

	// 使用多模态 LLM 生成描述
	desc, err := p.generateDescription(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("failed to generate image description: %w", err)
	}
	result.TextDescription = desc

	// 生成图像向量 (如果有 embedder)
	if p.embedder != nil {
		embedding, err := p.embedder.EmbedImage(ctx, content)
		if err != nil {
			return nil, fmt.Errorf("failed to embed image: %w", err)
		}
		result.Embedding = embedding
	}

	// 添加元数据
	result.Metadata["content_type"] = "image"
	result.Metadata["format"] = content.Format
	if len(content.Data) > 0 {
		result.Metadata["size_bytes"] = len(content.Data)
	}

	return result, nil
}

// generateDescription 生成图像描述
func (p *ImageProcessor) generateDescription(ctx context.Context, content *Content) (string, error) {
	// 构建多模态消息
	imageURL := content.URL
	if imageURL == "" {
		imageURL = content.ToBase64()
	}

	// 构建带图像的提示词
	// 注意: 实际实现需要根据 LLM provider 的多模态接口调整
	// 不同的 provider (GPT-4V, Claude Vision, Gemini) 有不同的图像传递方式
	prompt := fmt.Sprintf("%s\n\n[图像: %s]", p.defaultPrompt, imageURL)

	// 使用 vision 模型
	req := llm.CompletionRequest{
		Model: p.model,
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: prompt,
			},
		},
		// 在请求级别的 Metadata 中添加图像信息
		Metadata: map[string]any{
			"images": []string{imageURL},
		},
	}

	resp, err := p.provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

// SupportedTypes 支持的内容类型
func (p *ImageProcessor) SupportedTypes() []ContentType {
	return []ContentType{ContentTypeImage}
}

// ============== AudioProcessor 音频处理器 ==============

// AudioProcessor 音频处理器
type AudioProcessor struct {
	transcriber AudioTranscriber
}

// AudioTranscriber 音频转录接口
type AudioTranscriber interface {
	// Transcribe 将音频转录为文本
	Transcribe(ctx context.Context, audio *Content) (string, error)
}

// NewAudioProcessor 创建音频处理器
func NewAudioProcessor(transcriber AudioTranscriber) *AudioProcessor {
	return &AudioProcessor{
		transcriber: transcriber,
	}
}

// Process 处理音频
func (p *AudioProcessor) Process(ctx context.Context, content *Content) (*ProcessResult, error) {
	if !content.IsAudio() {
		return nil, fmt.Errorf("content is not audio")
	}

	result := &ProcessResult{
		Metadata: make(map[string]any),
	}

	// 转录音频
	if p.transcriber != nil {
		transcript, err := p.transcriber.Transcribe(ctx, content)
		if err != nil {
			return nil, fmt.Errorf("failed to transcribe audio: %w", err)
		}
		result.TextDescription = transcript
	}

	// 添加元数据
	result.Metadata["content_type"] = "audio"
	result.Metadata["format"] = content.Format
	if len(content.Data) > 0 {
		result.Metadata["size_bytes"] = len(content.Data)
	}

	return result, nil
}

// SupportedTypes 支持的内容类型
func (p *AudioProcessor) SupportedTypes() []ContentType {
	return []ContentType{ContentTypeAudio}
}

// ============== VideoProcessor 视频处理器 ==============

// VideoProcessor 视频处理器
type VideoProcessor struct {
	imageProcessor *ImageProcessor
	audioProcessor *AudioProcessor
	frameExtractor FrameExtractor
}

// FrameExtractor 帧提取器接口
type FrameExtractor interface {
	// ExtractFrames 从视频中提取关键帧
	ExtractFrames(ctx context.Context, video *Content, interval time.Duration) ([]*Content, error)

	// ExtractAudio 从视频中提取音频
	ExtractAudio(ctx context.Context, video *Content) (*Content, error)
}

// NewVideoProcessor 创建视频处理器
func NewVideoProcessor(imageProcessor *ImageProcessor, audioProcessor *AudioProcessor, frameExtractor FrameExtractor) *VideoProcessor {
	return &VideoProcessor{
		imageProcessor: imageProcessor,
		audioProcessor: audioProcessor,
		frameExtractor: frameExtractor,
	}
}

// Process 处理视频
func (p *VideoProcessor) Process(ctx context.Context, content *Content) (*ProcessResult, error) {
	if !content.IsVideo() {
		return nil, fmt.Errorf("content is not video")
	}

	result := &ProcessResult{
		Metadata: make(map[string]any),
	}

	var descriptions []string

	// 提取并处理关键帧
	if p.frameExtractor != nil && p.imageProcessor != nil {
		frames, err := p.frameExtractor.ExtractFrames(ctx, content, 5*time.Second)
		if err == nil {
			for i, frame := range frames {
				frameResult, err := p.imageProcessor.Process(ctx, frame)
				if err == nil {
					descriptions = append(descriptions, fmt.Sprintf("帧%d: %s", i+1, frameResult.TextDescription))
				}
			}
		}
	}

	// 提取并处理音频
	if p.frameExtractor != nil && p.audioProcessor != nil {
		audio, err := p.frameExtractor.ExtractAudio(ctx, content)
		if err == nil {
			audioResult, err := p.audioProcessor.Process(ctx, audio)
			if err == nil {
				descriptions = append(descriptions, "音频转录: "+audioResult.TextDescription)
			}
		}
	}

	result.TextDescription = strings.Join(descriptions, "\n\n")

	// 添加元数据
	result.Metadata["content_type"] = "video"
	result.Metadata["format"] = content.Format
	if len(content.Data) > 0 {
		result.Metadata["size_bytes"] = len(content.Data)
	}

	return result, nil
}

// SupportedTypes 支持的内容类型
func (p *VideoProcessor) SupportedTypes() []ContentType {
	return []ContentType{ContentTypeVideo}
}

// ============== MultimodalIndexer 多模态索引器 ==============

// MultimodalIndexer 多模态索引器
type MultimodalIndexer struct {
	store      vector.Store
	embedder   vector.Embedder
	processors map[ContentType]ContentProcessor
	batchSize  int
	mu         sync.RWMutex
}

// MultimodalIndexerOption 选项
type MultimodalIndexerOption func(*MultimodalIndexer)

// WithMultimodalBatchSize 设置批量大小
func WithMultimodalBatchSize(size int) MultimodalIndexerOption {
	return func(i *MultimodalIndexer) {
		i.batchSize = size
	}
}

// WithProcessor 添加内容处理器
func WithProcessor(processor ContentProcessor) MultimodalIndexerOption {
	return func(i *MultimodalIndexer) {
		for _, t := range processor.SupportedTypes() {
			i.processors[t] = processor
		}
	}
}

// NewMultimodalIndexer 创建多模态索引器
func NewMultimodalIndexer(store vector.Store, embedder vector.Embedder, opts ...MultimodalIndexerOption) *MultimodalIndexer {
	idx := &MultimodalIndexer{
		store:      store,
		embedder:   embedder,
		processors: make(map[ContentType]ContentProcessor),
		batchSize:  10,
	}
	for _, opt := range opts {
		opt(idx)
	}
	return idx
}

// IndexDocuments 索引多模态文档
func (i *MultimodalIndexer) IndexDocuments(ctx context.Context, docs []*MultimodalDocument) error {
	for _, doc := range docs {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// 处理各模态内容
		var allDescriptions []string
		for _, content := range doc.Contents {
			if content.IsText() {
				allDescriptions = append(allDescriptions, content.Text)
				continue
			}

			// 使用对应处理器处理
			processor, ok := i.processors[content.Type]
			if !ok {
				continue
			}

			result, err := processor.Process(ctx, content)
			if err != nil {
				continue // 跳过处理失败的内容
			}

			if result.TextDescription != "" {
				allDescriptions = append(allDescriptions, result.TextDescription)
			}

			if len(result.Embedding) > 0 {
				doc.Embeddings[content.Type] = result.Embedding
			}

			// 合并元数据
			if doc.Metadata == nil {
				doc.Metadata = make(map[string]any)
			}
			for k, v := range result.Metadata {
				doc.Metadata[k] = v
			}
		}

		// 更新文本描述
		doc.TextDescription = strings.Join(allDescriptions, "\n")

		// 生成文本向量
		if doc.TextDescription != "" {
			embeddings, err := i.embedder.Embed(ctx, []string{doc.TextDescription})
			if err != nil {
				return fmt.Errorf("failed to embed text: %w", err)
			}
			doc.Embeddings[ContentTypeText] = embeddings[0]
		}

		// 存储到向量数据库
		vectorDoc := vector.Document{
			ID:        doc.ID,
			Content:   doc.TextDescription,
			Embedding: doc.Embeddings[ContentTypeText],
			Metadata:  doc.Metadata,
			CreatedAt: doc.CreatedAt,
		}

		if err := i.store.Add(ctx, []vector.Document{vectorDoc}); err != nil {
			return fmt.Errorf("failed to store document: %w", err)
		}
	}

	return nil
}

// Index 实现 rag.Indexer 接口
func (i *MultimodalIndexer) Index(ctx context.Context, docs []rag.Document) error {
	// 将 RAG 文档转换为多模态文档
	mmDocs := make([]*MultimodalDocument, len(docs))
	for j, doc := range docs {
		mmDocs[j] = &MultimodalDocument{
			ID:       doc.ID,
			Contents: []*Content{NewTextContent(doc.Content)},
			Metadata: doc.Metadata,
		}
	}
	return i.IndexDocuments(ctx, mmDocs)
}

// Delete 删除文档
func (i *MultimodalIndexer) Delete(ctx context.Context, ids []string) error {
	return i.store.Delete(ctx, ids)
}

// Clear 清空索引
func (i *MultimodalIndexer) Clear(ctx context.Context) error {
	return i.store.Clear(ctx)
}

// Count 返回文档数量
func (i *MultimodalIndexer) Count(ctx context.Context) (int, error) {
	return i.store.Count(ctx)
}

var _ rag.Indexer = (*MultimodalIndexer)(nil)

// ============== MultimodalRetriever 多模态检索器 ==============

// MultimodalRetriever 多模态检索器
type MultimodalRetriever struct {
	store          vector.Store
	embedder       vector.Embedder
	imageEmbedder  ImageEmbedder
	imageProcessor *ImageProcessor
	topK           int
}

// MultimodalRetrieverOption 选项
type MultimodalRetrieverOption func(*MultimodalRetriever)

// WithMultimodalTopK 设置返回数量
func WithMultimodalTopK(k int) MultimodalRetrieverOption {
	return func(r *MultimodalRetriever) {
		r.topK = k
	}
}

// WithMultimodalImageEmbedder 设置图像向量化器
func WithMultimodalImageEmbedder(embedder ImageEmbedder) MultimodalRetrieverOption {
	return func(r *MultimodalRetriever) {
		r.imageEmbedder = embedder
	}
}

// WithMultimodalImageProcessor 设置图像处理器
func WithMultimodalImageProcessor(processor *ImageProcessor) MultimodalRetrieverOption {
	return func(r *MultimodalRetriever) {
		r.imageProcessor = processor
	}
}

// NewMultimodalRetriever 创建多模态检索器
func NewMultimodalRetriever(store vector.Store, embedder vector.Embedder, opts ...MultimodalRetrieverOption) *MultimodalRetriever {
	r := &MultimodalRetriever{
		store:    store,
		embedder: embedder,
		topK:     10,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RetrieveByText 基于文本查询检索
func (r *MultimodalRetriever) RetrieveByText(ctx context.Context, query string) ([]rag.Document, error) {
	// 生成查询向量
	embeddings, err := r.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	// 向量检索
	results, err := r.store.Search(ctx, embeddings[0], r.topK)
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	// 转换为 RAG 文档
	docs := make([]rag.Document, len(results))
	for i, result := range results {
		docs[i] = rag.Document{
			ID:       result.ID,
			Content:  result.Content,
			Metadata: result.Metadata,
			Score:    result.Score,
		}
	}

	return docs, nil
}

// RetrieveByImage 基于图像查询检索
func (r *MultimodalRetriever) RetrieveByImage(ctx context.Context, image *Content) ([]rag.Document, error) {
	var queryEmbedding []float32

	// 尝试使用图像向量化
	if r.imageEmbedder != nil {
		embedding, err := r.imageEmbedder.EmbedImage(ctx, image)
		if err == nil {
			queryEmbedding = embedding
		}
	}

	// 如果没有图像向量，使用图像描述进行文本检索
	if queryEmbedding == nil && r.imageProcessor != nil {
		result, err := r.imageProcessor.Process(ctx, image)
		if err != nil {
			return nil, fmt.Errorf("failed to process image: %w", err)
		}
		return r.RetrieveByText(ctx, result.TextDescription)
	}

	if queryEmbedding == nil {
		return nil, fmt.Errorf("no image embedder or processor available")
	}

	// 向量检索
	results, err := r.store.Search(ctx, queryEmbedding, r.topK)
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	// 转换为 RAG 文档
	docs := make([]rag.Document, len(results))
	for i, result := range results {
		docs[i] = rag.Document{
			ID:       result.ID,
			Content:  result.Content,
			Metadata: result.Metadata,
			Score:    result.Score,
		}
	}

	return docs, nil
}

// Retrieve 实现 rag.Retriever 接口
func (r *MultimodalRetriever) Retrieve(ctx context.Context, query string, topK int) ([]rag.Document, error) {
	oldTopK := r.topK
	r.topK = topK
	defer func() { r.topK = oldTopK }()
	return r.RetrieveByText(ctx, query)
}

// ============== MultimodalLoader 多模态加载器 ==============

// MultimodalLoader 多模态加载器
type MultimodalLoader struct {
	supportedExtensions map[string]ContentType
}

// NewMultimodalLoader 创建多模态加载器
func NewMultimodalLoader() *MultimodalLoader {
	return &MultimodalLoader{
		supportedExtensions: map[string]ContentType{
			".txt":  ContentTypeText,
			".md":   ContentTypeText,
			".html": ContentTypeText,
			".png":  ContentTypeImage,
			".jpg":  ContentTypeImage,
			".jpeg": ContentTypeImage,
			".gif":  ContentTypeImage,
			".webp": ContentTypeImage,
			".mp3":  ContentTypeAudio,
			".wav":  ContentTypeAudio,
			".ogg":  ContentTypeAudio,
			".mp4":  ContentTypeVideo,
			".webm": ContentTypeVideo,
		},
	}
}

// LoadFile 加载单个文件
func (l *MultimodalLoader) LoadFile(ctx context.Context, path string) (*MultimodalDocument, error) {
	ext := strings.ToLower(filepath.Ext(path))
	contentType, ok := l.supportedExtensions[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported file extension: %s", ext)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var content *Content
	switch contentType {
	case ContentTypeText:
		content = NewTextContent(string(data))
	case ContentTypeImage:
		var format ImageFormat
		switch ext {
		case ".png":
			format = ImageFormatPNG
		case ".jpg", ".jpeg":
			format = ImageFormatJPEG
		case ".gif":
			format = ImageFormatGIF
		case ".webp":
			format = ImageFormatWebP
		}
		content = NewImageContent(data, format)
	case ContentTypeAudio:
		var format AudioFormat
		switch ext {
		case ".mp3":
			format = AudioFormatMP3
		case ".wav":
			format = AudioFormatWAV
		case ".ogg":
			format = AudioFormatOGG
		}
		content = NewAudioContent(data, format)
	case ContentTypeVideo:
		var format VideoFormat
		switch ext {
		case ".mp4":
			format = VideoFormatMP4
		case ".webm":
			format = VideoFormatWebM
		}
		content = NewVideoContent(data, format)
	}

	doc := NewMultimodalDocument(content)
	doc.Metadata = map[string]any{
		"source": path,
		"type":   string(contentType),
	}

	return doc, nil
}

// LoadDirectory 加载目录中的所有文件
func (l *MultimodalLoader) LoadDirectory(ctx context.Context, dir string) ([]*MultimodalDocument, error) {
	var docs []*MultimodalDocument

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := l.supportedExtensions[ext]; !ok {
			return nil
		}

		doc, err := l.LoadFile(ctx, path)
		if err != nil {
			return nil // 跳过加载失败的文件
		}

		docs = append(docs, doc)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return docs, nil
}

// LoadFromReader 从 Reader 加载
func (l *MultimodalLoader) LoadFromReader(ctx context.Context, r io.Reader, contentType ContentType, format string) (*MultimodalDocument, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	var content *Content
	switch contentType {
	case ContentTypeText:
		content = NewTextContent(string(data))
	case ContentTypeImage:
		content = NewImageContent(data, ImageFormat(format))
	case ContentTypeAudio:
		content = NewAudioContent(data, AudioFormat(format))
	case ContentTypeVideo:
		content = NewVideoContent(data, VideoFormat(format))
	default:
		return nil, fmt.Errorf("unsupported content type: %s", contentType)
	}

	return NewMultimodalDocument(content), nil
}

// ============== CLIP 模型支持 ==============

// ErrCLIPNotImplemented 表示未注入 CLIP 后端时调用嵌入方法返回的错误。
var ErrCLIPNotImplemented = fmt.Errorf("CLIP embedding 未配置后端: 请用 WithCLIPBackend 注入真实 CLIP 后端（OpenAI CLIP / HF Inference / clip-as-service / 自建服务）")

// CLIPBackend 是 CLIP 向量化的外部后端接口。
//
// 框架不内置 CLIP 模型（需 GPU / 外部推理服务），而是通过本接口让用户注入真实后端：
//   - OpenAI / Hugging Face Inference API
//   - clip-as-service 或自建 CLIP gRPC/HTTP 服务
//
// 注入后 CLIPEmbedder 的方法委托给后端；未注入时返回 ErrCLIPNotImplemented。
// 这样框架在不内置重依赖、不伪造结果的前提下完整支持 CLIP 能力。
type CLIPBackend interface {
	// EmbedText 向量化文本，返回与图像向量同空间、可比较的向量。
	EmbedText(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedImage 向量化单张图像。
	EmbedImage(ctx context.Context, image *Content) ([]float32, error)
}

// CLIPEmbedder CLIP 模型向量化器：同时处理文本与图像，生成可跨模态比较的向量。
//
// 本身不含模型权重；实际计算由注入的 CLIPBackend 完成（见 WithCLIPBackend）。
// 未注入后端时所有嵌入方法返回 ErrCLIPNotImplemented（安全默认，不伪造向量）。
type CLIPEmbedder struct {
	endpoint string
	apiKey   string
	backend  CLIPBackend
}

// CLIPOption 配置 CLIPEmbedder。
type CLIPOption func(*CLIPEmbedder)

// WithCLIPBackend 注入真实 CLIP 后端。注入后 IsImplemented() 返回 true。
func WithCLIPBackend(b CLIPBackend) CLIPOption {
	return func(e *CLIPEmbedder) { e.backend = b }
}

// NewCLIPEmbedder 创建 CLIP 向量化器。
//
// endpoint/apiKey 供基于 HTTP 的后端实现按需取用；未通过 WithCLIPBackend 注入后端时，
// 嵌入方法返回 ErrCLIPNotImplemented。
func NewCLIPEmbedder(endpoint, apiKey string, opts ...CLIPOption) *CLIPEmbedder {
	e := &CLIPEmbedder{
		endpoint: endpoint,
		apiKey:   apiKey,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// IsImplemented 返回是否已注入可用的 CLIP 后端。
func (e *CLIPEmbedder) IsImplemented() bool {
	return e.backend != nil
}

// EmbedText 向量化文本（委托后端；未注入后端返回 ErrCLIPNotImplemented）。
func (e *CLIPEmbedder) EmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	if e.backend == nil {
		return nil, ErrCLIPNotImplemented
	}
	return e.backend.EmbedText(ctx, texts)
}

// EmbedImage 向量化图像（委托后端；未注入后端返回 ErrCLIPNotImplemented）。
func (e *CLIPEmbedder) EmbedImage(ctx context.Context, image *Content) ([]float32, error) {
	if e.backend == nil {
		return nil, ErrCLIPNotImplemented
	}
	return e.backend.EmbedImage(ctx, image)
}

// EmbedImages 批量向量化图像（逐张委托后端；未注入后端返回 ErrCLIPNotImplemented）。
func (e *CLIPEmbedder) EmbedImages(ctx context.Context, images []*Content) ([][]float32, error) {
	if e.backend == nil {
		return nil, ErrCLIPNotImplemented
	}
	out := make([][]float32, len(images))
	for i, img := range images {
		vec, err := e.backend.EmbedImage(ctx, img)
		if err != nil {
			return nil, err
		}
		out[i] = vec
	}
	return out, nil
}
