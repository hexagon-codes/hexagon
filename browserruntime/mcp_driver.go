package browserruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // 注册JPEG截图解码器。
	_ "image/png"  // 注册PNG截图解码器。
	"math"
	"strings"

	_ "golang.org/x/image/webp" // 注册WebP截图解码器。

	coretool "github.com/hexagon-codes/ai-core/tool"

	"github.com/hexagon-codes/hexagon/internal/util"
	hexagonmcp "github.com/hexagon-codes/hexagon/mcp"
	"time"
)

// ToolSchema 是现有MCP服务暴露的工具名称及输入合同。
type ToolSchema struct {
	Name   string
	Schema any
}

// MCPDriver 的回调由现有 MCP Manager 装配，所有调用明确指定同一个服务器。
type MCPDriver struct {
	ListTools func(context.Context, string) ([]ToolSchema, error)
	CallTool  func(context.Context, string, string, map[string]any) (coretool.Result, error)
	WaitIdle  func(context.Context, string) error
}

func (d *MCPDriver) tools(ctx context.Context, server string) (map[string]ToolSchema, error) {
	if d.ListTools == nil || d.CallTool == nil {
		return nil, errors.New("browser MCP callbacks are unavailable")
	}
	list, err := d.ListTools(ctx, server)
	if err != nil {
		return nil, err
	}
	tools := make(map[string]ToolSchema, len(list))
	for _, item := range list {
		tools[item.Name] = item
	}
	return tools, nil
}

func (d *MCPDriver) call(ctx context.Context, server, tool string, args map[string]any) (coretool.Result, error) {
	if err := ctx.Err(); err != nil {
		return coretool.Result{}, err
	}
	if args == nil {
		args = map[string]any{}
	}
	result, err := d.CallTool(ctx, server, tool, args)
	var observed *ObservedToolError
	if errors.As(err, &observed) {
		return result, err
	}
	if MCPResultIsError(result) {
		message := result.Error
		if message == "" && err != nil {
			message = err.Error()
		}
		return result, &ObservedToolError{Message: message}
	}
	if err != nil {
		return result, &UnknownCallError{Cause: err}
	}
	if !result.Success {
		return result, &ObservedToolError{Message: result.Error}
	}
	return result, nil
}

// MCPResultIsError 仅依据实际协议中的 isError，不能按错误文本猜测远端是否执行。
func MCPResultIsError(result coretool.Result) bool {
	return hexagonmcp.ResultIsError(result)
}

// ObservedToolError 表示收到了工具明确失败，不把它混同于传输结果未知。
type ObservedToolError struct{ Message string }

func (e *ObservedToolError) Error() string { return e.Message }

// UnknownCallError 表示动作已经派发但尚未取得可信结果。
type UnknownCallError struct{ Cause error }

func (e *UnknownCallError) Error() string {
	return fmt.Sprintf("browser operation outcome is unknown: %v", e.Cause)
}
func (e *UnknownCallError) Unwrap() error { return e.Cause }

func codeTool(tools map[string]ToolSchema) (string, error) {
	for _, name := range []string{"browser_run_code_unsafe", "browser_run_code"} {
		if _, ok := tools[name]; ok {
			return name, nil
		}
	}
	return "", errors.New("browser MCP does not provide same-page code execution required for stable page binding")
}

// 身份挂在同一 Playwright BrowserContext 的 Page 对象上；关闭或新建页不会复用旧 token。
const observePageCode = `async (page) => {
  const context = page.context();
  const key = Symbol.for('hexclaw.browser.identity');
  if (!context[key]) Object.defineProperty(context, key, { value: { id: Date.now().toString(36) + '-' + Math.random().toString(36).slice(2), next: 0, pages: new WeakMap(), closed: new Set() } });
  const registry = context[key];
  if (!registry.closed) registry.closed = new Set();
  if (!registry.aliases) registry.aliases = new WeakMap();
  if (!registry.trackedClosures) registry.trackedClosures = new WeakSet();
  const identity = p => { if (!registry.pages.has(p)) registry.pages.set(p,registry.id + ':' + (++registry.next)); const id=registry.pages.get(p); if(!registry.aliases.has(p)) registry.aliases.set(p,new Set()); const aliases=registry.aliases.get(p); aliases.add(id); if(!registry.trackedClosures.has(p)) { registry.trackedClosures.add(p); p.once('close',()=>{for(const known of aliases) registry.closed.add(known);}); } return id; };
  const pages = await Promise.all(context.pages().filter(p => !p.isClosed()).map(async (p, index) => ({ id: identity(p), index, url: p.url(), title: await p.title().catch(() => '') })));
  const size = page.viewportSize() || await page.evaluate(() => ({ width: window.innerWidth, height: window.innerHeight }));
  return { hexclaw_browser_observation: { browser_id: registry.id, page: pages.find(p => p.id === identity(page)), pages, width: size.width, height: size.height } };
}`

func (d *MCPDriver) observe(ctx context.Context, server, code string) (Observation, error) {
	result, err := d.call(ctx, server, code, map[string]any{"code": observePageCode})
	if err != nil {
		return Observation{}, err
	}
	return decodeObservation(result)
}

func decodeObservation(result coretool.Result) (Observation, error) {
	encoded, err := json.Marshal(result.Output)
	if err != nil {
		return Observation{}, errors.New("browser observation could not be decoded")
	}
	var observation Observation
	if findObservation(encoded, &observation) && observation.Page.ID != "" {
		return observation, nil
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err == nil && walkObservation(value, &observation) && observation.Page.ID != "" {
		return observation, nil
	}
	return Observation{}, errors.New("browser MCP returned no stable page observation")
}

// Inspect 只返回候选页面及稳定身份，多个标签页时由现有对话确定目标。
func (d *MCPDriver) Inspect(ctx context.Context, server string) (Observation, error) {
	tools, err := d.tools(ctx, server)
	if err != nil {
		return Observation{}, err
	}
	code, err := codeTool(tools)
	if err != nil {
		return Observation{}, err
	}
	return d.observe(ctx, server, code)
}

func walkObservation(value any, out *Observation) bool {
	switch v := value.(type) {
	case string:
		return findObservation([]byte(v), out)
	case []any:
		for _, item := range v {
			if walkObservation(item, out) {
				return true
			}
		}
	case map[string]any:
		if observed, ok := v["hexclaw_browser_observation"]; ok {
			data, err := json.Marshal(observed)
			return err == nil && json.Unmarshal(data, out) == nil
		}
		for _, key := range []string{"structuredContent", "content", "text", "result"} {
			if nested, ok := v[key]; ok && walkObservation(nested, out) {
				return true
			}
		}
	}
	return false
}

func findObservation(data []byte, out *Observation) bool {
	marker := []byte(`"hexclaw_browser_observation"`)
	index := bytes.Index(data, marker)
	if index < 0 {
		return false
	}
	start := bytes.LastIndexByte(data[:index], '{')
	if start < 0 {
		return false
	}
	var envelope struct {
		Observation Observation `json:"hexclaw_browser_observation"`
	}
	if json.NewDecoder(bytes.NewReader(data[start:])).Decode(&envelope) != nil {
		return false
	}
	*out = envelope.Observation
	return true
}

// Bind 创建任务页或按稳定 token 选择原页，原索引改变后也不能切到别的页面。
func (d *MCPDriver) Bind(ctx context.Context, server, pageID string, newPage bool) (Observation, error) {
	tools, err := d.tools(ctx, server)
	if err != nil {
		return Observation{}, err
	}
	code, err := codeTool(tools)
	if err != nil {
		return Observation{}, err
	}
	if newPage {
		if _, ok := tools["browser_tabs"]; !ok {
			return Observation{}, errors.New("browser tab creation is unsupported")
		}
		nonce := util.GenerateID("browser_page")
		encoded, _ := jsValue(nonce)
		createCode := `async (page) => { const created=await page.context().newPage(); Object.defineProperty(created,Symbol.for('hexclaw.browser.creation'),{value:` + encoded + `}); try { return await (` + observePageCode + `)(created); } catch (error) { await created.close(); throw error; } }`
		result, callErr := d.call(ctx, server, code, map[string]any{"code": createCode})
		observed, decodeErr := Observation{}, error(nil)
		if callErr == nil {
			observed, decodeErr = decodeObservation(result)
		}
		failure := errors.Join(callErr, decodeErr)
		if failure == nil {
			_, failure = d.call(ctx, server, "browser_tabs", map[string]any{"action": "select", "index": observed.Page.Index})
		}
		if failure == nil {
			var confirmed Observation
			confirmed, failure = d.observe(ctx, server, code)
			if failure == nil && confirmed.Page.ID != observed.Page.ID {
				failure = ErrSessionLost
			}
		}
		if failure != nil {
			var unknown *UnknownCallError
			if errors.As(failure, &unknown) {
				return Observation{}, failure
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			cleanupCode := `async (page) => { const target=page.context().pages().find(p=>p[Symbol.for('hexclaw.browser.creation')]===` + encoded + `); if(target&&!target.isClosed()) await target.close(); return {hexclaw_closed_page:true}; }`
			if _, cleanupErr := d.call(cleanupCtx, server, code, map[string]any{"code": cleanupCode}); cleanupErr != nil {
				return Observation{}, &UnknownCallError{Cause: errors.Join(failure, cleanupErr)}
			}
			return Observation{}, failure
		}
		return observed, nil
	}
	observed, err := d.observe(ctx, server, code)
	if err != nil {
		return Observation{}, err
	}
	if pageID == "" {
		if len(observed.Pages) != 1 {
			return Observation{}, ErrTargetRequired
		}
		pageID = observed.Pages[0].ID
	}
	if observed.Page.ID == pageID {
		return observed, nil
	}
	index := -1
	for _, page := range observed.Pages {
		if page.ID == pageID {
			index = page.Index
			break
		}
	}
	if index < 0 {
		return Observation{}, ErrSessionLost
	}
	if _, ok := tools["browser_tabs"]; !ok {
		return Observation{}, errors.New("browser tab selection is unsupported")
	}
	if _, err = d.call(ctx, server, "browser_tabs", map[string]any{"action": "select", "index": index}); err != nil {
		return Observation{}, err
	}
	observed, err = d.observe(ctx, server, code)
	if err != nil {
		return Observation{}, err
	}
	if observed.Page.ID != pageID {
		return Observation{}, ErrSessionLost
	}
	return observed, nil
}

func jsValue(value any) (string, error) { data, err := json.Marshal(value); return string(data), err }

func validCoordinate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

// Execute 经原MCP连接执行一次浏览器动作并返回实际观察。
func (d *MCPDriver) Execute(ctx context.Context, ref SessionRef, action Action) (ActionResult, error) {
	tools, err := d.tools(ctx, ref.ServerName)
	if err != nil {
		return ActionResult{}, err
	}
	name, args, err := actionTool(tools, action)
	if err != nil {
		return ActionResult{}, err
	}
	result, err := d.call(ctx, ref.ServerName, name, args)
	out := ActionResult{Result: result, Outcome: "observed"}
	if err != nil {
		var observed *ObservedToolError
		if !errors.As(err, &observed) {
			out.Outcome = "unknown"
		}
		return out, err
	}
	out.FileChooserPending = fileChooserFromResult(result.Output)
	if out.FileChooserPending {
		return out, nil
	}
	if action.Type == "login_ready" {
		ready, parseErr := loginReadyFromResult(result.Output)
		if parseErr != nil {
			return out, parseErr
		}
		out.Ready = &ready
	}
	if action.Type == "screenshot" || action.Type == "mcp" && action.ToolName == "browser_take_screenshot" {
		if filename, ok := action.Arguments["filename"].(string); ok && action.Type == "mcp" && filename != "" {
			// 上游显式文件截图只返回文件链接，原字节由应用的文件产物桥读取。
			return out, nil
		}
		out.Images, err = imagesFromResult(result.Output)
		if err != nil {
			return out, err
		}
		if len(out.Images) == 0 {
			return out, errors.New("browser screenshot returned no image bytes")
		}
	}
	if action.Type == "mcp" && action.ToolName == "browser_tabs" {
		if operation, ok := action.Arguments["action"].(string); !ok || operation != "list" {
			code, codeErr := codeTool(tools)
			if codeErr != nil {
				return out, codeErr
			}
			observation, observeErr := d.observe(ctx, ref.ServerName, code)
			if observeErr != nil {
				return out, fmt.Errorf("browser tab action completed but observation failed: %w", observeErr)
			}
			out.Observation = &observation
		}
	}
	return out, nil
}

// 官方模态节独立于页面快照；不把页面代码块中的同名文本当成模态事实。
func fileChooserFromResult(output any) bool {
	data, err := json.Marshal(output)
	if err != nil {
		return false
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(data, &result) != nil {
		return false
	}
	for _, block := range result.Content {
		if block.Type != "text" {
			continue
		}
		modal, fenced := false, false
		for _, line := range strings.Split(block.Text, "\n") {
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			if heading, ok := strings.CutPrefix(line, "### "); ok {
				modal = heading == "Modal state"
				continue
			}
			if modal && line == "- [File chooser]: can be handled by browser_file_upload" {
				return true
			}
		}
	}
	return false
}

func actionTool(tools map[string]ToolSchema, action Action) (toolName string, arguments map[string]any, resultErr error) {
	args := make(map[string]any)
	name := ""
	code := ""
	point := func() error {
		if !validCoordinate(action.X) || !validCoordinate(action.Y) {
			return errors.New("browser coordinates must be finite non-negative CSS pixels")
		}
		return nil
	}
	switch action.Type {
	case "login_ready":
		condition, err := jsValue(action.Arguments["ready"])
		if err != nil {
			return "", nil, err
		}
		// 只接受数据值，检测代码固定；不把调用参数解释为可执行 JavaScript。
		code = `async (page) => { const ready = ` + condition + `; return await page.evaluate((ready) => {
  const checks = [];
  if (ready.url) checks.push(location.href === ready.url);
  if (ready.text) checks.push((document.body?.innerText || '').includes(ready.text));
  if (ready.selector) checks.push([...document.querySelectorAll(ready.selector)].some(element => {
    const style = getComputedStyle(element);
    return style.visibility !== 'hidden' && style.display !== 'none' && element.getClientRects().length > 0;
  }));
  if (!checks.length) throw new Error('A verifiable login ready condition is required');
  return { hexclaw_login_ready: checks.every(Boolean) };
}, ready); }`
	case "screenshot":
		name = "browser_take_screenshot"
		args["type"] = "png"
		if schemaHasProperty(tools[name], "scale") {
			args["scale"] = "css"
		}
	case "click", "double_click":
		if err := point(); err != nil {
			return "", nil, err
		}
		name = "browser_mouse_click_xy"
		args["x"], args["y"] = action.X, action.Y
		button := action.Button
		if button == "" {
			button = "left"
		}
		if button == "wheel" {
			button = "middle"
		}
		if button != "left" && button != "middle" && button != "right" {
			return "", nil, errors.New("unsupported browser mouse button")
		}
		args["button"] = button
		if action.Type == "double_click" {
			args["clickCount"] = 2
		}
	case "move":
		if err := point(); err != nil {
			return "", nil, err
		}
		name = "browser_mouse_move_xy"
		args["x"], args["y"] = action.X, action.Y
	case "scroll":
		if err := point(); err != nil {
			return "", nil, err
		}
		if math.IsNaN(action.ScrollX) || math.IsNaN(action.ScrollY) || math.IsInf(action.ScrollX, 0) || math.IsInf(action.ScrollY, 0) {
			return "", nil, errors.New("browser scroll deltas must be finite")
		}
		code = fmt.Sprintf("async (page) => { await page.mouse.move(%g,%g); await page.mouse.wheel(%g,%g); }", action.X, action.Y, action.ScrollX, action.ScrollY)
	case "drag":
		if len(action.Path) < 2 {
			return "", nil, errors.New("browser drag requires at least two points")
		}
		for _, p := range action.Path {
			if !validCoordinate(p.X) || !validCoordinate(p.Y) {
				return "", nil, errors.New("browser drag coordinates must be finite non-negative CSS pixels")
			}
		}
		path, encodeErr := jsValue(action.Path)
		if encodeErr != nil {
			return "", nil, encodeErr
		}
		code = "async (page) => { const points = " + path + "; await page.mouse.move(points[0].x,points[0].y); await page.mouse.down(); try { for (const p of points.slice(1)) await page.mouse.move(p.x,p.y); } finally { await page.mouse.up(); } }"
	case "type":
		text, encodeErr := jsValue(action.Text)
		if encodeErr != nil {
			return "", nil, encodeErr
		}
		code = "async (page) => { await page.keyboard.insertText(" + text + "); }"
	case "keypress":
		if len(action.Keys) == 0 {
			return "", nil, errors.New("browser keypress requires a key")
		}
		name = "browser_press_key"
		keys := append([]string(nil), action.Keys...)
		for i, key := range keys {
			switch strings.ToUpper(key) {
			case "CTRL", "CONTROL":
				keys[i] = "Control"
			case "ALT":
				keys[i] = "Alt"
			case "SHIFT":
				keys[i] = "Shift"
			case "META", "CMD", "COMMAND", "SUPER":
				keys[i] = "Meta"
			case "RETURN", "ENTER":
				keys[i] = "Enter"
			case "ESC", "ESCAPE":
				keys[i] = "Escape"
			case "TAB":
				keys[i] = "Tab"
			case "BACKSPACE":
				keys[i] = "Backspace"
			case "DELETE":
				keys[i] = "Delete"
			case "SPACE":
				keys[i] = "Space"
			case "ARROWLEFT":
				keys[i] = "ArrowLeft"
			case "ARROWRIGHT":
				keys[i] = "ArrowRight"
			case "ARROWUP":
				keys[i] = "ArrowUp"
			case "ARROWDOWN":
				keys[i] = "ArrowDown"
			case "HOME":
				keys[i] = "Home"
			case "END":
				keys[i] = "End"
			case "PAGEUP":
				keys[i] = "PageUp"
			case "PAGEDOWN":
				keys[i] = "PageDown"
			}
			if keys[i] == "" {
				return "", nil, errors.New("browser key cannot be empty")
			}
		}
		args["key"] = strings.Join(keys, "+")
	case "wait":
		if action.Seconds < 0 || math.IsNaN(action.Seconds) || math.IsInf(action.Seconds, 0) {
			return "", nil, errors.New("browser wait duration must be finite and non-negative")
		}
		name = "browser_wait_for"
		args["time"] = action.Seconds
	case "navigate":
		if strings.TrimSpace(action.URL) == "" {
			return "", nil, errors.New("browser navigation URL is required")
		}
		name = "browser_navigate"
		args["url"] = action.URL
	case "upload":
		name = "browser_file_upload"
		args["paths"] = action.Paths
	case "mcp":
		name = action.ToolName
		args = action.Arguments
	default:
		return "", nil, fmt.Errorf("unsupported browser action %q", action.Type)
	}
	if code != "" {
		var err error
		name, err = codeTool(tools)
		if err != nil {
			return "", nil, err
		}
		args = map[string]any{"code": code}
	}
	if _, ok := tools[name]; !ok {
		return "", nil, fmt.Errorf("browser MCP capability %q is unavailable", name)
	}
	return name, args, nil
}

func loginReadyFromResult(output any) (bool, error) {
	data, err := json.Marshal(output)
	if err != nil {
		return false, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return false, err
	}
	var find func(any) (bool, bool)
	find = func(value any) (bool, bool) {
		switch value := value.(type) {
		case map[string]any:
			if ready, ok := value["hexclaw_login_ready"].(bool); ok {
				return ready, true
			}
			for _, key := range []string{"structuredContent", "content", "text", "result"} {
				if ready, found := find(value[key]); found {
					return ready, true
				}
			}
		case []any:
			for _, item := range value {
				if ready, found := find(item); found {
					return ready, true
				}
			}
		case string:
			index := strings.Index(value, `"hexclaw_login_ready"`)
			if index < 0 {
				break
			}
			start := strings.LastIndex(value[:index], "{")
			if start < 0 {
				break
			}
			var decoded map[string]any
			if json.NewDecoder(strings.NewReader(value[start:])).Decode(&decoded) == nil {
				return find(decoded)
			}
		}
		return false, false
	}
	if ready, found := find(value); found {
		return ready, nil
	}
	return false, errors.New("browser MCP returned no login ready condition result")
}

func schemaHasProperty(tool ToolSchema, name string) bool {
	data, err := json.Marshal(tool.Schema)
	if err != nil {
		return false
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(data, &schema) != nil {
		return false
	}
	_, ok := schema.Properties[name]
	return ok
}

func imagesFromResult(output any) ([]Image, error) {
	contents, err := hexagonmcp.ToolImageContents(output)
	if err != nil {
		return nil, err
	}
	var images []Image
	for _, content := range contents {
		config, _, decodeErr := image.DecodeConfig(bytes.NewReader(content.Data))
		if decodeErr != nil {
			return nil, errors.New("browser screenshot dimensions could not be decoded")
		}
		images = append(images, Image{MIME: content.MIMEType, Data: content.Data, Width: config.Width, Height: config.Height})
	}
	return images, nil
}

// Quiesce 等待原MCP会话中在途浏览器动作停止。
func (d *MCPDriver) Quiesce(ctx context.Context, server string) error {
	if d.WaitIdle == nil {
		return ErrNotQuiescent
	}
	return d.WaitIdle(ctx, server)
}

// ClosePage 在同一服务内按 Page 对象 token 关闭，避免索引重排后误关用户页面。
func (d *MCPDriver) ClosePage(ctx context.Context, server, pageID string) (coretool.Result, error) {
	if pageID == "" {
		return coretool.Result{}, errors.New("stable browser page identity is required")
	}
	tools, err := d.tools(ctx, server)
	if err != nil {
		return coretool.Result{}, err
	}
	tool, err := codeTool(tools)
	if err != nil {
		return coretool.Result{}, err
	}
	encoded, err := jsValue(pageID)
	if err != nil {
		return coretool.Result{}, err
	}
	code := `async (page) => { const context = page.context(); const registry = context[Symbol.for('hexclaw.browser.identity')]; if (!registry) return { hexclaw_closed_page: false, reason:'identity_unavailable' }; const id=` + encoded + `; const target=context.pages().find(p=>registry.pages.get(p)===id); if(!target) return {hexclaw_closed_page: !!registry.closed?.has(id), reason:registry.closed?.has(id)?'already_closed':'identity_not_found'}; if(!target.isClosed()) await target.close(); if(registry.closed) registry.closed.add(id); return {hexclaw_closed_page:true}; }`
	result, err := d.call(ctx, server, tool, map[string]any{"code": code})
	if err != nil {
		return result, err
	}
	data, err := json.Marshal(result.Output)
	if err != nil {
		return result, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return result, err
	}
	closed, found := pageClosure(value)
	if !found || !closed {
		return result, &ObservedToolError{Message: "browser page closure was not confirmed"}
	}
	return result, nil
}

// pageClosure读取明确的关闭事实，不把工具调用成功当成页面已经关闭。
func pageClosure(value any) (bool, bool) {
	switch v := value.(type) {
	case map[string]any:
		if closed, ok := v["hexclaw_closed_page"].(bool); ok {
			return closed, true
		}
		for _, key := range []string{"structuredContent", "content", "text", "result"} {
			if nested, ok := v[key]; ok {
				if closed, found := pageClosure(nested); found {
					return closed, true
				}
			}
		}
	case []any:
		for _, nested := range v {
			if closed, found := pageClosure(nested); found {
				return closed, true
			}
		}
	case string:
		data := []byte(v)
		index := bytes.Index(data, []byte(`"hexclaw_closed_page"`))
		if index < 0 {
			return false, false
		}
		start := bytes.LastIndexByte(data[:index], '{')
		if start < 0 {
			return false, false
		}
		var object map[string]any
		if json.NewDecoder(bytes.NewReader(data[start:])).Decode(&object) == nil {
			return pageClosure(object)
		}
	}
	return false, false
}
