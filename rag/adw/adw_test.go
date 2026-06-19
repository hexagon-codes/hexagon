package adw

import (
	"context"
	"testing"
	"time"

	"github.com/hexagon-codes/hexagon/rag"
)

// TestNewDocument 测试创建文档
func TestNewDocument(t *testing.T) {
	ragDoc := rag.Document{
		ID:      "test-1",
		Content: "这是一份测试文档",
		Source:  "test.txt",
	}

	doc := NewDocument(ragDoc)

	if doc.Document.ID != "test-1" {
		t.Errorf("ID = %v, want test-1", doc.Document.ID)
	}

	if doc.Type != DocTypeUnknown {
		t.Errorf("Type = %v, want unknown", doc.Type)
	}

	if len(doc.StructuredData) != 0 {
		t.Error("StructuredData should be empty")
	}
}

// TestDocumentStructuredData 测试结构化数据操作
func TestDocumentStructuredData(t *testing.T) {
	doc := NewDocument(rag.Document{})

	// 设置值
	doc.SetStructuredValue("name", "张三")
	doc.SetStructuredValue("age", 30)
	doc.SetStructuredValue("active", true)

	// 获取值
	name, ok := doc.GetStructuredValue("name")
	if !ok || name != "张三" {
		t.Errorf("name = %v, want 张三", name)
	}

	age, ok := doc.GetStructuredValue("age")
	if !ok || age != 30 {
		t.Errorf("age = %v, want 30", age)
	}

	// 获取不存在的值
	_, ok = doc.GetStructuredValue("nonexistent")
	if ok {
		t.Error("不存在的键应该返回 false")
	}
}

// TestDocumentAddTable 测试添加表格
func TestDocumentAddTable(t *testing.T) {
	doc := NewDocument(rag.Document{})

	table := Table{
		ID:      "table-1",
		Name:    "销售数据",
		Headers: []string{"产品", "数量", "价格"},
		Rows: [][]string{
			{"产品A", "100", "10.00"},
			{"产品B", "200", "20.00"},
		},
	}

	doc.AddTable(table)

	if len(doc.Tables) != 1 {
		t.Errorf("Tables count = %d, want 1", len(doc.Tables))
	}

	if doc.Tables[0].Name != "销售数据" {
		t.Errorf("Table name = %v, want 销售数据", doc.Tables[0].Name)
	}
}

// TestTableOperations 测试表格操作
func TestTableOperations(t *testing.T) {
	table := Table{
		Headers: []string{"A", "B", "C"},
		Rows: [][]string{
			{"1", "2", "3"},
			{"4", "5", "6"},
		},
	}

	// 行列数
	if table.RowCount() != 2 {
		t.Errorf("RowCount() = %d, want 2", table.RowCount())
	}

	if table.ColCount() != 3 {
		t.Errorf("ColCount() = %d, want 3", table.ColCount())
	}

	// 获取单元格
	if table.GetCell(0, 1) != "2" {
		t.Errorf("GetCell(0, 1) = %v, want 2", table.GetCell(0, 1))
	}

	// 越界
	if table.GetCell(10, 10) != "" {
		t.Error("越界应该返回空字符串")
	}

	// 转换为 map
	maps := table.ToMap()
	if len(maps) != 2 {
		t.Errorf("ToMap() length = %d, want 2", len(maps))
	}

	if maps[0]["A"] != "1" {
		t.Errorf("maps[0][A] = %v, want 1", maps[0]["A"])
	}
}

// TestDocumentAddEntity 测试添加实体
func TestDocumentAddEntity(t *testing.T) {
	doc := NewDocument(rag.Document{})

	entity := Entity{
		ID:         "entity-1",
		Text:       "北京市",
		Type:       EntityLocation,
		Confidence: 0.95,
	}

	doc.AddEntity(entity)

	if len(doc.Entities) != 1 {
		t.Errorf("Entities count = %d, want 1", len(doc.Entities))
	}

	if doc.Entities[0].Type != EntityLocation {
		t.Errorf("Entity type = %v, want location", doc.Entities[0].Type)
	}
}

// TestDocumentValidation 测试文档验证
func TestDocumentValidation(t *testing.T) {
	doc := NewDocument(rag.Document{})

	// 初始应该是有效的
	if !doc.IsValid() {
		t.Error("新文档应该是有效的")
	}

	// 添加验证错误
	doc.AddValidationError(ValidationError{
		Field:    "amount",
		Rule:     "required",
		Message:  "金额是必需的",
		Severity: SeverityError,
	})

	if doc.IsValid() {
		t.Error("有验证错误的文档应该是无效的")
	}
}

// TestDocumentProcessingHistory 测试处理历史
func TestDocumentProcessingHistory(t *testing.T) {
	doc := NewDocument(rag.Document{})

	step := ProcessingStep{
		StepName:  "test_step",
		StartTime: time.Now(),
		EndTime:   time.Now().Add(time.Second),
		Duration:  time.Second,
		Success:   true,
	}

	doc.AddProcessingStep(step)

	if len(doc.ProcessingHistory) != 1 {
		t.Errorf("ProcessingHistory count = %d, want 1", len(doc.ProcessingHistory))
	}

	if doc.ProcessingHistory[0].StepName != "test_step" {
		t.Errorf("StepName = %v, want test_step", doc.ProcessingHistory[0].StepName)
	}
}

// TestExtractionSchema 测试提取 Schema
func TestExtractionSchema(t *testing.T) {
	schema := NewExtractionSchema("invoice").
		AddStringField("invoice_number", "发票号码", true).
		AddDateField("date", "日期", "YYYY-MM-DD", true).
		AddMoneyField("amount", "金额", true).
		AddStringField("vendor", "供应商", false)

	if schema.Name != "invoice" {
		t.Errorf("Name = %v, want invoice", schema.Name)
	}

	if len(schema.Fields) != 4 {
		t.Errorf("Fields count = %d, want 4", len(schema.Fields))
	}

	if len(schema.Required) != 3 {
		t.Errorf("Required count = %d, want 3", len(schema.Required))
	}

	// 获取字段
	field := schema.GetField("amount")
	if field == nil {
		t.Fatal("GetField(amount) 返回 nil")
	}

	if field.Type != FieldTypeMoney {
		t.Errorf("field.Type = %v, want money", field.Type)
	}

	// 获取不存在的字段
	if schema.GetField("nonexistent") != nil {
		t.Error("不存在的字段应该返回 nil")
	}
}

// TestProcessOptions 测试处理选项
func TestProcessOptions(t *testing.T) {
	opts := DefaultProcessOptions()

	if !opts.EnableTableExtraction {
		t.Error("EnableTableExtraction should be true by default")
	}

	if !opts.EnableEntityRecognition {
		t.Error("EnableEntityRecognition should be true by default")
	}

	if opts.MaxConcurrency != 4 {
		t.Errorf("MaxConcurrency = %d, want 4", opts.MaxConcurrency)
	}
}

// TestPipelineCreation 测试管道创建
func TestPipelineCreation(t *testing.T) {
	pipeline := NewPipeline("test-pipeline").
		WithDescription("测试管道").
		AddStep(NewDocumentTypeDetectorStep()).
		AddStep(NewTextNormalizerStep()).
		AddStep(NewConfidenceCalculatorStep()).
		Build()

	if pipeline.Name() != "test-pipeline" {
		t.Errorf("Name() = %v, want test-pipeline", pipeline.Name())
	}

	steps := pipeline.GetSteps()
	if len(steps) != 3 {
		t.Errorf("Steps count = %d, want 3", len(steps))
	}
}

// TestPipelineProcess 测试管道处理
func TestPipelineProcess(t *testing.T) {
	// 创建简单管道
	pipeline := NewPipeline("simple-pipeline").
		AddStep(NewDocumentTypeDetectorStep()).
		AddStep(NewTextNormalizerStep()).
		AddStep(NewConfidenceCalculatorStep()).
		Build()

	// 创建测试文档
	docs := []rag.Document{
		{
			ID:      "doc-1",
			Content: "这是一份发票，金额：1000元",
			Source:  "invoice.pdf",
		},
		{
			ID:      "doc-2",
			Content: "这是一份合同",
			Source:  "contract.docx",
		},
	}

	// 处理
	ctx := context.Background()
	output, err := pipeline.Process(ctx, PipelineInput{
		Documents: docs,
		Options:   DefaultProcessOptions(),
	})

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if output.TotalDocuments != 2 {
		t.Errorf("TotalDocuments = %d, want 2", output.TotalDocuments)
	}

	if output.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", output.SuccessCount)
	}

	if len(output.Documents) != 2 {
		t.Errorf("Documents count = %d, want 2", len(output.Documents))
	}

	// 检查文档类型检测
	for _, doc := range output.Documents {
		if doc.Type == DocTypeUnknown {
			t.Log("文档类型检测结果: unknown (可能是预期的)")
		}

		// 检查处理历史
		if len(doc.ProcessingHistory) != 3 {
			t.Errorf("ProcessingHistory count = %d, want 3", len(doc.ProcessingHistory))
		}
	}
}

// TestPipelineWithHooks 测试管道钩子
func TestPipelineWithHooks(t *testing.T) {
	var startCalled, endCalled bool
	var stepStartCount, stepEndCount int

	hooks := &PipelineHooks{
		OnStart: func(ctx context.Context, input *PipelineInput) {
			startCalled = true
		},
		OnEnd: func(ctx context.Context, output *PipelineOutput, err error) {
			endCalled = true
		},
		OnStepStart: func(ctx context.Context, step Step, doc *Document) {
			stepStartCount++
		},
		OnStepEnd: func(ctx context.Context, step Step, doc *Document, err error) {
			stepEndCount++
		},
	}

	pipeline := NewPipeline("hook-test").
		WithHooks(hooks).
		AddStep(NewDocumentTypeDetectorStep()).
		Build()

	ctx := context.Background()
	_, err := pipeline.Process(ctx, PipelineInput{
		Documents: []rag.Document{{ID: "1", Content: "test"}},
	})

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if !startCalled {
		t.Error("OnStart hook 未被调用")
	}

	if !endCalled {
		t.Error("OnEnd hook 未被调用")
	}

	if stepStartCount != 1 {
		t.Errorf("OnStepStart 调用次数 = %d, want 1", stepStartCount)
	}

	if stepEndCount != 1 {
		t.Errorf("OnStepEnd 调用次数 = %d, want 1", stepEndCount)
	}
}

// TestFuncStep 测试函数步骤
func TestFuncStep(t *testing.T) {
	executed := false

	step := NewFuncStep("test-step", func(ctx context.Context, doc *Document, opts ProcessOptions) error {
		executed = true
		doc.SetStructuredValue("test_key", "test_value")
		return nil
	})

	if step.Name() != "test-step" {
		t.Errorf("Name() = %v, want test-step", step.Name())
	}

	if !step.CanHandle(nil) {
		t.Error("CanHandle should return true by default")
	}

	doc := NewDocument(rag.Document{})
	err := step.Process(context.Background(), doc, ProcessOptions{})

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if !executed {
		t.Error("步骤未被执行")
	}

	value, _ := doc.GetStructuredValue("test_key")
	if value != "test_value" {
		t.Errorf("test_key = %v, want test_value", value)
	}
}

// TestConditionalStep 测试条件步骤
func TestConditionalStep(t *testing.T) {
	executedCount := 0

	step := NewConditionalStep(
		"conditional-step",
		func(doc *Document) bool {
			return doc.Type == DocTypePDF
		},
		func(ctx context.Context, doc *Document, opts ProcessOptions) error {
			executedCount++
			return nil
		},
	)

	ctx := context.Background()

	// PDF 文档应该被处理
	pdfDoc := NewDocument(rag.Document{})
	pdfDoc.Type = DocTypePDF

	if !step.CanHandle(pdfDoc) {
		t.Error("PDF 文档应该能被处理")
	}

	step.Process(ctx, pdfDoc, ProcessOptions{})

	// 非 PDF 文档不应该被处理
	textDoc := NewDocument(rag.Document{})
	textDoc.Type = DocTypeText

	if step.CanHandle(textDoc) {
		t.Error("Text 文档不应该被处理")
	}

	if executedCount != 1 {
		t.Errorf("executedCount = %d, want 1", executedCount)
	}
}

// TestDocumentTypeDetector 测试文档类型检测
func TestDocumentTypeDetector(t *testing.T) {
	step := NewDocumentTypeDetectorStep()

	tests := []struct {
		source       string
		expectedType DocumentType
	}{
		{"document.pdf", DocTypePDF},
		{"document.docx", DocTypeWord},
		{"document.xlsx", DocTypeExcel},
		{"image.jpg", DocTypeImage},
		{"page.html", DocTypeHTML},
		{"data.json", DocTypeJSON},
		{"file.txt", DocTypeText},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			doc := NewDocument(rag.Document{Source: tt.source})
			err := step.Process(ctx, doc, ProcessOptions{})

			if err != nil {
				t.Fatalf("Process() error = %v", err)
			}

			if doc.Type != tt.expectedType {
				t.Errorf("Type = %v, want %v", doc.Type, tt.expectedType)
			}
		})
	}
}

// TestEntityTypes 测试实体类型
func TestEntityTypes(t *testing.T) {
	types := []EntityType{
		EntityPerson,
		EntityOrganization,
		EntityLocation,
		EntityDate,
		EntityTime,
		EntityMoney,
		EntityPercent,
		EntityEmail,
		EntityPhone,
		EntityURL,
		EntityProduct,
		EntityEvent,
		EntityCustom,
	}

	for _, et := range types {
		if et == "" {
			t.Errorf("EntityType 不应为空")
		}
	}
}

// TestFieldTypes 测试字段类型
func TestFieldTypes(t *testing.T) {
	types := []FieldType{
		FieldTypeString,
		FieldTypeNumber,
		FieldTypeInteger,
		FieldTypeBoolean,
		FieldTypeArray,
		FieldTypeObject,
		FieldTypeDate,
		FieldTypeTime,
		FieldTypeMoney,
		FieldTypeEmail,
		FieldTypePhone,
		FieldTypeURL,
	}

	for _, ft := range types {
		if ft == "" {
			t.Errorf("FieldType 不应为空")
		}
	}
}

// TestSeverityTypes 测试严重程度类型
func TestSeverityTypes(t *testing.T) {
	if SeverityError != "error" {
		t.Errorf("SeverityError = %v, want error", SeverityError)
	}

	if SeverityWarning != "warning" {
		t.Errorf("SeverityWarning = %v, want warning", SeverityWarning)
	}

	if SeverityInfo != "info" {
		t.Errorf("SeverityInfo = %v, want info", SeverityInfo)
	}
}

// BenchmarkPipelineProcess 基准测试管道处理
func BenchmarkPipelineProcess(b *testing.B) {
	pipeline := NewPipeline("bench-pipeline").
		AddStep(NewDocumentTypeDetectorStep()).
		AddStep(NewTextNormalizerStep()).
		AddStep(NewConfidenceCalculatorStep()).
		Build()

	docs := []rag.Document{
		{ID: "1", Content: "测试文档内容", Source: "test.txt"},
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pipeline.Process(ctx, PipelineInput{Documents: docs})
	}
}

// BenchmarkDocumentAddEntity 基准测试添加实体
func BenchmarkDocumentAddEntity(b *testing.B) {
	doc := NewDocument(rag.Document{})

	entity := Entity{
		ID:         "entity-1",
		Text:       "北京市",
		Type:       EntityLocation,
		Confidence: 0.95,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc.AddEntity(entity)
	}
}
