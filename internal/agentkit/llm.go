// Package agentkit 从用户配置构建 ADK Agent。 本文件为 OpenAI 兼容的 Chat
// Completions API 和 Anthropic Messages API 实现 model.LLM
// 适配器，二者均支持流式和函数调用，因此可以配置任意供应商。
package agentkit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// httpClient 由两个适配器共享。流式响应受请求上下文约束，绝不受客户端级超时约束。空客户端
// 是有意为之：它使用 http.DefaultTransport，以便 ApplyGlobalProxy 生效。
var httpClient = &http.Client{}

func readErrBody(body io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(body, 4096))
	return strings.TrimSpace(string(b))
}

// ---------------------------------------------------------------------------
// genai 辅助函数，由两个适配器共享

type adkFunctionDecl struct {
	name        string
	description string
	parameters  map[string]any
}

func configTools(req *model.LLMRequest) []adkFunctionDecl {
	var out []adkFunctionDecl
	if req.Config == nil {
		return out
	}
	for _, t := range req.Config.Tools {
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			out = append(out, adkFunctionDecl{
				name:        fd.Name,
				description: fd.Description,
				parameters:  functionParameters(fd),
			})
		}
	}
	return out
}

// functionParameters 解析声明中的 JSON Schema。ADK 的内置工具（functiontool）和
// MCP 工具集通过 ParametersJsonSchema 发布其 schema；仅读取遗留的 genai.Schema
// 字段会把每个工具都向 OpenAI 兼容/Anthropic 供应商描述为无参数的
// {"type":"object"}，导致模型无法以正确的参数调用工作区、技能或 MCP 工具（issue
// #23）。
func functionParameters(fd *genai.FunctionDeclaration) map[string]any {
	if fd.ParametersJsonSchema != nil {
		if m, ok := jsonSchemaMap(fd.ParametersJsonSchema); ok {
			return m
		}
	}
	return schemaToJSONSchema(fd.Parameters)
}

// jsonSchemaMap 将 JSON schema 载荷（jsonschema-go 结构体、MCP 工具集使用的
// map[string]any，或任何其他可 JSON 序列化的值）规范化为普通对象。
func jsonSchemaMap(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok && len(m) > 0 {
		return m, true
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || len(m) == 0 {
		return nil, false
	}
	return m, true
}

func schemaToJSONSchema(s *genai.Schema) map[string]any {
	if s == nil {
		return map[string]any{"type": "object"}
	}
	m := map[string]any{}
	if s.Type != "" {
		m["type"] = strings.ToLower(string(s.Type))
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if s.Items != nil {
		m["items"] = schemaToJSONSchema(s.Items)
	}
	if len(s.Properties) > 0 {
		props := map[string]any{}
		for k, v := range s.Properties {
			props[k] = schemaToJSONSchema(v)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	return m
}

func systemText(req *model.LLMRequest) string {
	if req.Config == nil || req.Config.SystemInstruction == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range req.Config.SystemInstruction.Parts {
		if p != nil && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func temperatureOf(req *model.LLMRequest) *float32 {
	if req.Config == nil {
		return nil
	}
	return req.Config.Temperature
}

func maxTokensOf(req *model.LLMRequest) int64 {
	if req.Config == nil {
		return 0
	}
	return int64(req.Config.MaxOutputTokens)
}

// stopSequencesOf 暴露已配置的停止序列（未设置时为 nil）。
func stopSequencesOf(req *model.LLMRequest) []string {
	if req.Config == nil {
		return nil
	}
	return req.Config.StopSequences
}

// withUsage 将供应商用量附加到最终响应，以便 ADK runner 存储它，并让 UI 能显示
// token 计数。
func withUsage(resp *model.LLMResponse, u *oaUsage) *model.LLMResponse {
	if resp == nil || u == nil {
		return resp
	}
	resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        int32(u.PromptTokens),
		CandidatesTokenCount:    int32(u.CompletionTokens),
		TotalTokenCount:         int32(u.TotalTokens),
		CachedContentTokenCount: int32(u.cachedPrompt()),
	}
	return resp
}

func withUsageA(resp *model.LLMResponse, u *anUsage) *model.LLMResponse {
	if resp == nil || u == nil {
		return resp
	}
	total := u.InputTokens + u.OutputTokens
	// 在 Anthropic API 上，cache_read_input_tokens 是 input
	// 的子集，因此它绝不会改变提示词总量——它仅告诉 UI 其中有多少是由缓存提供的。
	resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        int32(u.InputTokens),
		CandidatesTokenCount:    int32(u.OutputTokens),
		TotalTokenCount:         int32(total),
		CachedContentTokenCount: int32(u.CacheReadInputTokens),
	}
	return resp
}

// argsToMap 解析工具调用参数。出现错误意味着流式参数片段从未形成有效
// JSON（流被截断、行被丢弃）；调用方不得以零值参数调用工具——静默地以空参数运行
// 写工具，比显式地让回合失败更糟。
func argsToMap(raw []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// appendBrokenArgsNote 为流式参数始终未能构成合法 JSON 的工具调用追加一条用户
// 可见提示（这些调用被跳过，未执行）。
func appendBrokenArgsNote(text string, skipped []string) string {
	if len(skipped) == 0 {
		return text
	}
	sep := ""
	if text != "" {
		sep = "\n\n"
	}
	return text + fmt.Sprintf("%s（工具调用 %s 的参数流损坏，已跳过本次执行，请重试。）", sep, strings.Join(skipped, ", "))
}

// finalLLMResponse 组装 ADK Runner
// 处理的单个非部分事件：完整文本加上每个函数调用。
func finalLLMResponse(modelName, text string, parts []*genai.Part) *model.LLMResponse {
	return finalLLMResponseWithThought(modelName, "", text, parts)
}

// finalLLMResponseWithThought 组装 ADK Runner
// 处理的单个非部分事件。推理部分放在最前面，使历史记录将思考块渲染在答案及其工
// 具调用之上。
func finalLLMResponseWithThought(modelName, thought, text string, parts []*genai.Part) *model.LLMResponse {
	all := make([]*genai.Part, 0, len(parts)+2)
	if thought != "" {
		all = append(all, &genai.Part{Text: thought, Thought: true})
	}
	if text != "" || len(all) == 0 {
		all = append(all, &genai.Part{Text: text})
	}
	all = append(all, parts...)
	return &model.LLMResponse{
		ModelVersion: modelName,
		Content:      &genai.Content{Role: genai.RoleModel, Parts: all},
		TurnComplete: true,
	}
}

func textDelta(modelName, delta string) *model.LLMResponse {
	return &model.LLMResponse{
		ModelVersion: modelName,
		Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: delta}}},
		Partial:      true,
	}
}

// thoughtDelta 将一段思考内容作为 Thought 部分流式输出。UI 会把这些内容渲染到
// 可折叠的「思考过程」块中，而不是答案气泡里。
func thoughtDelta(modelName, delta string) *model.LLMResponse {
	return &model.LLMResponse{
		ModelVersion: modelName,
		Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: delta, Thought: true}}},
		Partial:      true,
	}
}

func scanSSE(body io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	return sc
}

// ---------------------------------------------------------------------------
// 两个适配器共享的流式响应管道

// streamIdleTimeout 限制打开的流可以静默多久。没有它时，供应商/网关停止发送
// 字节却从不关闭响应，会让回合一直停留在「正在回复…」直到整轮截止时间
// （默认 10 分钟）——这正是 issue #23 报告的症状。
var streamIdleTimeout = 2 * time.Minute

// startStreamWatchdog 将请求上下文转换为在无活动持续 `d`
// 后取消的上下文。调用方每收到一行都会重置定时器，因此仅真正停滞的流才会触发它
// 。流结束时必须运行 stop；它不会把正常完成路径变成失败。
func startStreamWatchdog(parent context.Context, d time.Duration) (context.Context, *time.Timer, *atomic.Bool, func()) {
	ctx, cancel := context.WithCancel(parent)
	var idle atomic.Bool
	timer := time.AfterFunc(d, func() {
		idle.Store(true)
		cancel()
	})
	stop := func() {
		timer.Stop()
		cancel()
	}
	return ctx, timer, &idle, stop
}

// streamFailure 将传输错误转换为最有用的消息：调用方自身的取消优先，其次是流看门狗触发，最后
// 是原始错误。
func streamFailure(parent context.Context, idle *atomic.Bool, fallback error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if idle != nil && idle.Load() {
		return fmt.Errorf("模型供应商超过 %s 没有返回任何数据，已中断本轮（请检查网络或代理后重试）", streamIdleTimeout)
	}
	return fallback
}

// bodyKind 对模型响应体的开头进行分类。
type bodyKind int

const (
	bodySSE bodyKind = iota
	bodyJSON
)

// sniffBody 报告响应体以什么开头，并回放其消耗的每一个字节。普通 JSON
// 文档（有些网关会以单个 JSON
// 对象响应流式请求，通常是其上游失效时返回的错误）会整体解码；其余内容都按 SSE
// 扫描，其中未识别的行会作为噪声跳过。将 JSON 错误对象按 SSE
// 处理曾导致回合静默结束且没有内容。
func sniffBody(body io.Reader) (bodyKind, io.Reader, error) {
	br := bufio.NewReaderSize(body, 64*1024)
	var prefix []byte
	for {
		line, err := br.ReadBytes('\n')
		prefix = append(prefix, line...)
		trimmed := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("\xef\xbb\xbf")))
		if len(trimmed) > 0 {
			replay := io.MultiReader(bytes.NewReader(prefix), br)
			if trimmed[0] == '{' || trimmed[0] == '[' {
				return bodyJSON, replay, nil
			}
			return bodySSE, replay, nil
		}
		if err != nil || len(prefix) > 64*1024 {
			// EOF（空响应体）或出现不合理的一连串空行：停止嗅探，并让 SSE
			// 扫描器处理剩余内容。
			return bodySSE, io.MultiReader(bytes.NewReader(prefix), br), err
		}
	}
}

// bodySnippet 返回意外响应体的简短、可打印摘录，用于错误消息。
func bodySnippet(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// OpenAI Chat Completions 兼容适配器

// OpenAICompatConfig 配置 OpenAI 兼容适配器。
type OpenAICompatConfig struct {
	BaseURL string // 例如 https://api.deepseek.com/v1
	APIKey  string
	Model   string
	// ExtraBody 携带已解析的模型级思考字段（reasoning_effort、thinking、...）。这些字段会合
	// 并进每个请求。
	ExtraBody map[string]any
}

type openaiCompatLLM struct {
	cfg OpenAICompatConfig
}

// NewOpenAICompat 构建一个遵循 OpenAI Chat Completions 协议的
// model.LLM（OpenAI、DeepSeek、Kimi、Qwen、Ollama、vLLM、...）。
func NewOpenAICompat(cfg OpenAICompatConfig) (model.LLM, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("OpenAI 兼容: 缺少模型 ID")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &openaiCompatLLM{cfg: cfg}, nil
}

func (o *openaiCompatLLM) Name() string { return o.cfg.Model }

type oaToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content,omitempty"` // 字符串或 content-part 数组
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
}

type oaContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

type oaRequest struct {
	Model         string      `json:"model"`
	Messages      []oaMessage `json:"messages"`
	Stream        bool        `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Tools       []oaTool `json:"tools,omitempty"`
	MaxTokens   int64    `json:"max_tokens,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	// ExtraBody 会在类型化字段之后被合并进 JSON 对象。
	ExtraBody map[string]any `json:"-"`
}

// MarshalJSON 保留类型化请求结构，并将 ExtraBody（thinking 级别）合并到顶层。
func (r oaRequest) MarshalJSON() ([]byte, error) {
	type alias oaRequest
	return marshalWithExtra(alias(r), r.ExtraBody)
}

type oaUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	// PromptTokensDetails 承载提示词中已缓存的部分。转发上游缓存命中的 OpenAI
	// 兼容网关会在此处报告该部分（有些网关则使用扁平的 "cached_tokens" 字段，由
	// oaUsage.cached 处理）。
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
	CachedTokens int64 `json:"cached_tokens,omitempty"`
}

// cachedPrompt 是提示词中已缓存的部分（当供应商未报告缓存时为
// 0）。实际中见到的两种拼写均可接受。
func (u *oaUsage) cachedPrompt() int64 {
	if u == nil {
		return 0
	}
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		return u.PromptTokensDetails.CachedTokens
	}
	return u.CachedTokens
}

type oaTool struct {
	Type     string    `json:"type"`
	Function oaToolDef `json:"function"`
}
type oaToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// oaReasoningText 接受供应商用于推理文本的各种形式：DeepSeek 将
// reasoning_content 作为普通字符串流式传输，有些网关则改用
// reasoning。值为对象的字段会被忽略，而不是导致整个数据块失败并丢弃同一 delta
// 中携带的答案。
type oaReasoningText string

func (r *oaReasoningText) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" || b[0] != '"' {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*r = oaReasoningText(s)
	return nil
}

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content          string          `json:"content"`
			ReasoningContent oaReasoningText `json:"reasoning_content"`
			Reasoning        oaReasoningText `json:"reasoning"`
			ToolCalls        []oaToolCall    `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *oaUsage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (o *openaiCompatLLM) buildMessages(req *model.LLMRequest) []oaMessage {
	var msgs []oaMessage
	if sys := systemText(req); sys != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: sys})
	}
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		var textParts []string
		var calls []oaToolCall
		var responses []*genai.FunctionResponse
		var images []*genai.Blob
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.InlineData != nil && p.InlineData.Data != nil:
				images = append(images, p.InlineData)
			case p.FunctionCall != nil:
				args, _ := json.Marshal(p.FunctionCall.Args)
				id := p.FunctionCall.ID
				if id == "" {
					id = p.FunctionCall.Name
				}
				tc := oaToolCall{ID: id, Type: "function"}
				tc.Function.Name = p.FunctionCall.Name
				tc.Function.Arguments = string(args)
				calls = append(calls, tc)
			case p.FunctionResponse != nil:
				responses = append(responses, p.FunctionResponse)
			case p.Text != "" && !p.Thought:
				// 推理内容仅用于显示：DeepSeek 会在下一个请求中拒绝其自身的
				// reasoning_content，重放它还会使提示词膨胀。
				textParts = append(textParts, p.Text)
			}
		}
		switch {
		case len(responses) > 0:
			for _, fr := range responses {
				payload, _ := json.Marshal(fr.Response)
				if len(fr.Response) == 0 {
					payload = []byte(`{"ok":true}`)
				}
				id := fr.ID
				if id == "" {
					id = fr.Name
				}
				msgs = append(msgs, oaMessage{Role: "tool", ToolCallID: id, Name: fr.Name, Content: string(payload)})
			}
		case len(calls) > 0:
			m := oaMessage{Role: "assistant", ToolCalls: calls}
			if joined := strings.Join(textParts, ""); joined != "" {
				m.Content = joined
			}
			msgs = append(msgs, m)
		default:
			joined := strings.Join(textParts, "")
			role := "user"
			if c.Role == genai.RoleModel {
				role = "assistant"
			}
			if joined == "" && len(images) == 0 {
				continue
			}
			if len(images) > 0 {
				var parts []oaContentPart
				if joined != "" {
					parts = append(parts, oaContentPart{Type: "text", Text: joined})
				}
				for _, b := range images {
					p := oaContentPart{Type: "image_url"}
					p.ImageURL = &struct {
						URL string `json:"url"`
					}{URL: "data:" + b.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(b.Data)}
					parts = append(parts, p)
				}
				msgs = append(msgs, oaMessage{Role: role, Content: parts})
			} else {
				msgs = append(msgs, oaMessage{Role: role, Content: joined})
			}
		}
	}
	return msgs
}

func (o *openaiCompatLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		parentCtx := ctx
		var idleTimer *time.Timer
		var idle *atomic.Bool
		if stream {
			var stop func()
			ctx, idleTimer, idle, stop = startStreamWatchdog(parentCtx, streamIdleTimeout)
			defer stop()
		}

		body := oaRequest{Model: o.cfg.Model, Messages: o.buildMessages(req), Stream: stream,
			MaxTokens: maxTokensOf(req), Temperature: temperatureOf(req), Stop: stopSequencesOf(req),
			ExtraBody: o.cfg.ExtraBody}
		if stream {
			body.StreamOptions = &struct {
				IncludeUsage bool `json:"include_usage"`
			}{IncludeUsage: true}
		}
		for _, fd := range configTools(req) {
			body.Tools = append(body.Tools, oaTool{Type: "function", Function: oaToolDef{
				Name: fd.name, Description: fd.description, Parameters: fd.parameters,
			}})
		}
		resp, err := o.post(ctx, body)
		if err != nil {
			yield(nil, streamFailure(parentCtx, idle, err))
			return
		}
		defer resp.Body.Close()

		if !stream {
			final, err := o.fullResponse(resp.Body)
			if err != nil {
				yield(nil, err)
				return
			}
			yield(final, nil)
			return
		}

		kind, bodyReader, serr := sniffBody(resp.Body)
		if serr != nil {
			if serr == io.EOF {
				yield(nil, fmt.Errorf("模型供应商 %s 返回空响应", o.cfg.Model))
				return
			}
			yield(nil, streamFailure(parentCtx, idle, serr))
			return
		}
		if kind == bodyJSON {
			// 有些网关会忽略
			// "stream":true，并返回一个普通文档（当其上游失效时，通常是一个错误对象）。
			// 解析它能让失败保持可见，而不是在没有内容的情况下结束该回合。
			final, err := o.fullResponse(bodyReader)
			if err != nil {
				yield(nil, streamFailure(parentCtx, idle, err))
				return
			}
			yield(final, nil)
			return
		}

		var usage *oaUsage
		var sb strings.Builder
		var tb strings.Builder
		calls := map[int]*oaAccumCall{}
		var order []int
		// 前导的非 SSE
		// 行会作为噪声被跳过（部分网关会前置状态文本），但记住第一行可以让
		// HTML/纯文本的失败页面以错误形式暴露出来，而不是返回空答案。
		var noise strings.Builder
		sawData := false

		scanner := scanSSE(bodyReader)
		for scanner.Scan() {
			if idleTimer != nil {
				idleTimer.Reset(streamIdleTimeout)
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				if !sawData && noise.Len() < 512 {
					if remain := 512 - noise.Len(); len(line) > remain {
						line = line[:remain]
					}
					noise.WriteString(line)
					noise.WriteString("\n")
				}
				continue
			}
			sawData = true
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				// [DONE] 是协议终止符。改为等待
				// EOF，则每当网关/代理在最后一个分块之后保持响应不关闭时，就会挂起回合（iss
				// ue #23）。
				break
			}
			var chunk oaChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if chunk.Error != nil && chunk.Error.Message != "" {
				yield(nil, fmt.Errorf("模型供应商错误（OpenAI 兼容）: %s", chunk.Error.Message))
				return
			}
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			d := chunk.Choices[0].Delta
			reasoning := string(d.ReasoningContent)
			if reasoning == "" {
				reasoning = string(d.Reasoning)
			}
			if reasoning != "" {
				tb.WriteString(reasoning)
				if !yield(thoughtDelta(o.cfg.Model, reasoning), nil) {
					return
				}
			}
			if d.Content != "" {
				sb.WriteString(d.Content)
				if !yield(textDelta(o.cfg.Model, d.Content), nil) {
					return
				}
			}
			for _, tc := range d.ToolCalls {
				oaAccumulate(calls, &order, tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments)
			}
		}
		if err := scanner.Err(); err != nil {
			yield(nil, streamFailure(parentCtx, idle, err))
			return
		}
		if sb.Len() == 0 && len(order) == 0 {
			if !sawData && strings.TrimSpace(noise.String()) != "" {
				yield(nil, fmt.Errorf("模型供应商 %s 返回了非 SSE 响应（HTTP 200）: %s",
					o.cfg.Model, bodySnippet(noise.String())))
				return
			}
			yield(nil, fmt.Errorf("模型供应商 %s 返回空响应", o.cfg.Model))
			return
		}
		yield(withUsage(o.final(tb.String(), sb.String(), calls, order), usage), nil)
	}
}

// oaFullResponse 是完整的（非 SSE）Chat Completions 响应。
type oaFullResponse struct {
	Choices []struct {
		Message struct {
			Content          string          `json:"content"`
			ReasoningContent oaReasoningText `json:"reasoning_content"`
			Reasoning        oaReasoningText `json:"reasoning"`
			ToolCalls        []oaToolCall    `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *oaUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// fullResponse 解码完整的 chat
// completion，暴露供应商错误对象，并拒绝无答案的载荷，而不是静默结束回合。
func (o *openaiCompatLLM) fullResponse(r io.Reader) (*model.LLMResponse, error) {
	var full oaFullResponse
	if err := json.NewDecoder(io.LimitReader(r, 8<<20)).Decode(&full); err != nil {
		return nil, fmt.Errorf("解析模型响应失败: %w", err)
	}
	if full.Error != nil && full.Error.Message != "" {
		return nil, fmt.Errorf("模型供应商错误（OpenAI 兼容）: %s", full.Error.Message)
	}
	if len(full.Choices) == 0 {
		return nil, fmt.Errorf("模型供应商 %s 返回空响应", o.cfg.Model)
	}
	var sb strings.Builder
	var tb strings.Builder
	calls := map[int]*oaAccumCall{}
	var order []int
	sb.WriteString(full.Choices[0].Message.Content)
	thought := string(full.Choices[0].Message.ReasoningContent)
	if thought == "" {
		thought = string(full.Choices[0].Message.Reasoning)
	}
	tb.WriteString(thought)
	for i, tc := range full.Choices[0].Message.ToolCalls {
		// 完整响应不携带逐调用索引；切片位置是唯一的排序依据。使用 tc.Index
		// 会把所有调用都合并到索引 0。
		oaAccumulate(calls, &order, i, tc.ID, tc.Function.Name, tc.Function.Arguments)
	}
	return withUsage(o.final(tb.String(), sb.String(), calls, order), full.Usage), nil
}

// oaAccumulate 按索引对流式工具调用片段进行分组。
func oaAccumulate(calls map[int]*oaAccumCall, order *[]int, idx int, id, name, argDelta string) *oaAccumCall {
	if idx < 0 || idx > 1024 {
		return nil // 供应商索引损坏/异常；忽略该片段
	}
	a := calls[idx]
	if a == nil {
		a = &oaAccumCall{id: id, name: name}
		calls[idx] = a
		*order = append(*order, idx)
	}
	if id != "" {
		a.id = id
	}
	if name != "" {
		a.name = name
	}
	a.args.WriteString(argDelta)
	return a
}

func (o *openaiCompatLLM) final(thought, text string, calls map[int]*oaAccumCall, order []int) *model.LLMResponse {
	var parts []*genai.Part
	var skipped []string
	for _, idx := range order {
		c := calls[idx]
		args, err := argsToMap([]byte(c.args.String()))
		if err != nil {
			// 参数流损坏：用 {} 执行工具会变成静默的零值调用。改为在回复文本中暴露出来。
			skipped = append(skipped, c.name)
			continue
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: c.id, Name: c.name, Args: args}})
	}
	return finalLLMResponseWithThought(o.cfg.Model, thought, appendBrokenArgsNote(text, skipped), parts)
}

type oaAccumCall struct {
	id, name string
	args     strings.Builder
}

func (o *openaiCompatLLM) post(ctx context.Context, body oaRequest) (*http.Response, error) {
	payload, _ := json.Marshal(body)
	build := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if o.cfg.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
		}
		return httpReq, nil
	}
	resp, err := postWithRetry(ctx, build, o.cfg.Model, "openai-compatible")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("OpenAI 兼容 %s: HTTP %d: %s", o.cfg.Model, resp.StatusCode, readErrBody(resp.Body))
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Anthropic Messages 适配器

// AnthropicConfig 用于配置 Anthropic 适配器。
type AnthropicConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	// ExtraBody 承载已解析的模型级 thinking
	// 字段（thinking、output_config、...）。这些字段会合并到每个请求中。
	ExtraBody map[string]any
}

type anthropicLLM struct {
	cfg AnthropicConfig
}

// NewAnthropic 构建一个使用 Anthropic Messages API 的 model.LLM。
func NewAnthropic(cfg AnthropicConfig) (model.LLM, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("Anthropic: 缺少模型 ID")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &anthropicLLM{cfg: cfg}, nil
}

func (a *anthropicLLM) Name() string { return a.cfg.Model }

type anBlock struct {
	Type string `json:"type"`
	// Text 文本
	Text string `json:"text,omitempty"`
	// ID 为 tool_use。
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
	// ToolUseID 对应 tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	// Source 图像
	Source *anImageSource `json:"source,omitempty"`
}

type anImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anMessage struct {
	Role    string    `json:"role"`
	Content []anBlock `json:"content"`
}

type anRequest struct {
	Model     string      `json:"model"`
	Messages  []anMessage `json:"messages"`
	System    string      `json:"system,omitempty"`
	MaxTokens int64       `json:"max_tokens"`
	Tools     []anTool    `json:"tools,omitempty"`
	Stream    bool        `json:"stream"`
	Temp      *float32    `json:"temperature,omitempty"`
	// StopSequences 将 genai 配置映射到 Anthropic 的 stop_sequences。
	StopSequences []string `json:"stop_sequences,omitempty"`
	// ExtraBody 会在类型化字段之后合并到 JSON 对象中。
	ExtraBody map[string]any `json:"-"`
}

// MarshalJSON 保留类型化请求结构，并将 ExtraBody（思考层级）合并到顶层。
func (r anRequest) MarshalJSON() ([]byte, error) {
	type alias anRequest
	return marshalWithExtra(alias(r), r.ExtraBody)
}

type anTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	// CacheReadInputTokens 是从 Anthropic
	// 的提示词缓存中提供的提示词前缀；CacheCreationInputTokens
	// 是本次调用写入该缓存的内容。
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

func (a *anthropicLLM) buildMessages(req *model.LLMRequest) []anMessage {
	var msgs []anMessage
	appendBlock := func(role genai.Role, b anBlock) {
		if len(msgs) > 0 && msgs[len(msgs)-1].Role == string(role) {
			msgs[len(msgs)-1].Content = append(msgs[len(msgs)-1].Content, b)
		} else {
			msgs = append(msgs, anMessage{Role: string(role), Content: []anBlock{b}})
		}
	}
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		var role genai.Role = genai.RoleUser
		if c.Role == genai.RoleModel {
			role = genai.RoleModel
		}
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.InlineData != nil && p.InlineData.Data != nil:
				appendBlock(role, anBlock{Type: "image", Source: &anImageSource{
					Type:      "base64",
					MediaType: p.InlineData.MIMEType,
					Data:      base64.StdEncoding.EncodeToString(p.InlineData.Data),
				}})
			case p.FunctionCall != nil:
				id := p.FunctionCall.ID
				if id == "" {
					id = "call_" + p.FunctionCall.Name
				}
				appendBlock(genai.RoleModel, anBlock{Type: "tool_use", ID: id, Name: p.FunctionCall.Name, Input: p.FunctionCall.Args})
			case p.FunctionResponse != nil:
				id := p.FunctionResponse.ID
				if id == "" {
					id = "call_" + p.FunctionResponse.Name
				}
				payload, _ := json.Marshal(p.FunctionResponse.Response)
				if len(p.FunctionResponse.Response) == 0 {
					payload = []byte(`{"ok":true}`)
				}
				appendBlock(genai.RoleUser, anBlock{Type: "tool_result", ToolUseID: id, Content: string(payload)})
			case p.Text != "" && !p.Thought:
				appendBlock(role, anBlock{Type: "text", Text: p.Text})
			}
		}
	}
	if len(msgs) > 0 && msgs[0].Role == string(genai.RoleModel) {
		msgs = append([]anMessage{{Role: string(genai.RoleUser), Content: []anBlock{{Type: "text", Text: "(start)"}}}}, msgs...)
	}
	return msgs
}

func (a *anthropicLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		parentCtx := ctx
		var idleTimer *time.Timer
		var idle *atomic.Bool
		if stream {
			var stop func()
			ctx, idleTimer, idle, stop = startStreamWatchdog(parentCtx, streamIdleTimeout)
			defer stop()
		}

		maxTok := maxTokensOf(req)
		if maxTok <= 0 {
			maxTok = 8192
		}
		body := anRequest{Model: a.cfg.Model, Messages: a.buildMessages(req), System: systemText(req),
			MaxTokens: maxTok, Stream: stream, Temp: temperatureOf(req), StopSequences: stopSequencesOf(req),
			ExtraBody: a.cfg.ExtraBody}
		for _, fd := range configTools(req) {
			body.Tools = append(body.Tools, anTool{
				Name: fd.name, Description: fd.description, InputSchema: fd.parameters,
			})
		}
		resp, err := a.post(ctx, body)
		if err != nil {
			yield(nil, streamFailure(parentCtx, idle, err))
			return
		}
		defer resp.Body.Close()

		if !stream {
			final, err := a.fullResponse(resp.Body)
			if err != nil {
				yield(nil, err)
				return
			}
			yield(final, nil)
			return
		}

		kind, bodyReader, serr := sniffBody(resp.Body)
		if serr != nil {
			if serr == io.EOF {
				yield(nil, fmt.Errorf("模型供应商 %s 返回空响应", a.cfg.Model))
				return
			}
			yield(nil, streamFailure(parentCtx, idle, serr))
			return
		}
		if kind == bodyJSON {
			final, err := a.fullResponse(bodyReader)
			if err != nil {
				yield(nil, streamFailure(parentCtx, idle, err))
				return
			}
			yield(final, nil)
			return
		}

		var usage *anUsage
		var sb strings.Builder
		var tools []*anAccumTool
		var noise strings.Builder
		sawData := false
		// Anthropic 会为内容块建立索引；恶意/损坏的端点可能发送负数或离谱的索引（通过无界
		// append 引发 panic / OOM）。
		const maxContentBlocks = 1024
		getTool := func(idx int) *anAccumTool {
			if idx < 0 || idx >= maxContentBlocks {
				return nil
			}
			for len(tools) <= idx {
				tools = append(tools, nil)
			}
			return tools[idx]
		}

		scanner := scanSSE(bodyReader)
	scan:
		for scanner.Scan() {
			if idleTimer != nil {
				idleTimer.Reset(streamIdleTimeout)
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				if !sawData && noise.Len() < 512 {
					if remain := 512 - noise.Len(); len(line) > remain {
						line = line[:remain]
					}
					noise.WriteString(line)
					noise.WriteString("\n")
				}
				continue
			}
			sawData = true
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			var ev struct {
				Type  string `json:"type"`
				Index int    `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
				ContentBlock struct {
					Type  string          `json:"type"`
					ID    string          `json:"id"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content_block"`
				Message *struct {
					Usage *anUsage `json:"usage"`
				} `json:"message"`
				Usage *anUsage `json:"usage"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			switch ev.Type {
			case "message_stop":
				// 协议终止符。若改为等待
				// EOF，则只要网关在最终事件后保持连接开启，就会挂起回合（issue #23）。
				break scan
			case "error":
				msg := "unknown error"
				if ev.Error != nil {
					msg = ev.Error.Message
				}
				yield(nil, fmt.Errorf("模型供应商错误（Anthropic）: %s", msg))
				return
			case "message_start":
				if ev.Message != nil && ev.Message.Usage != nil {
					usage = &anUsage{
						InputTokens:              ev.Message.Usage.InputTokens,
						CacheReadInputTokens:     ev.Message.Usage.CacheReadInputTokens,
						CacheCreationInputTokens: ev.Message.Usage.CacheCreationInputTokens,
					}
				}
			case "message_delta":
				if ev.Usage == nil {
					break
				}
				// 某些代理会丢弃 message_start；在此合成累加器，以便仍能统计输出 token。
				if usage == nil {
					usage = &anUsage{}
				}
				usage.OutputTokens = ev.Usage.OutputTokens
				// 缓存计数器可能挂在 message_delta 而非 message_start
				// 上（代理实现不同）；保留实际到达的那个。
				if ev.Usage.CacheReadInputTokens > 0 {
					usage.CacheReadInputTokens = ev.Usage.CacheReadInputTokens
				}
				if ev.Usage.CacheCreationInputTokens > 0 {
					usage.CacheCreationInputTokens = ev.Usage.CacheCreationInputTokens
				}
			case "content_block_start":
				if ev.ContentBlock.Type == "tool_use" {
					if ev.Index < 0 || ev.Index >= maxContentBlocks {
						continue
					}
					getTool(ev.Index) // 预留槽位
					tools[ev.Index] = &anAccumTool{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
				}
			case "content_block_delta":
				switch ev.Delta.Type {
				case "text_delta":
					if ev.Delta.Text != "" {
						sb.WriteString(ev.Delta.Text)
						if !yield(textDelta(a.cfg.Model, ev.Delta.Text), nil) {
							return
						}
					}
				case "input_json_delta":
					if t := getTool(ev.Index); t != nil {
						t.input.WriteString(ev.Delta.PartialJSON)
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			yield(nil, streamFailure(parentCtx, idle, err))
			return
		}
		if sb.Len() == 0 && !anyAnTool(tools) {
			if !sawData && strings.TrimSpace(noise.String()) != "" {
				yield(nil, fmt.Errorf("模型供应商 %s 返回了非 SSE 响应（HTTP 200）: %s",
					a.cfg.Model, bodySnippet(noise.String())))
				return
			}
			yield(nil, fmt.Errorf("模型供应商 %s 返回空响应", a.cfg.Model))
			return
		}
		yield(withUsageA(a.final(sb.String(), tools), usage), nil)
	}
}

// anFullResponse 是完整的（非 SSE）Anthropic Messages 响应。
type anFullResponse struct {
	Content []anBlock `json:"content"`
	Usage   *anUsage  `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// fullResponse 解码完整的 Messages 响应。非流式响应（A2A 执行器路径）也携带
// usage——没有它，服务端的 token 核算曾为空。
func (a *anthropicLLM) fullResponse(r io.Reader) (*model.LLMResponse, error) {
	var full anFullResponse
	if err := json.NewDecoder(io.LimitReader(r, 8<<20)).Decode(&full); err != nil {
		return nil, fmt.Errorf("解析模型响应失败: %w", err)
	}
	if full.Error != nil && full.Error.Message != "" {
		return nil, fmt.Errorf("模型供应商错误（Anthropic）: %s", full.Error.Message)
	}
	if len(full.Content) == 0 {
		return nil, fmt.Errorf("模型供应商 %s 返回空响应", a.cfg.Model)
	}
	var sb strings.Builder
	var tools []*anAccumTool
	for _, b := range full.Content {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)
		case "tool_use":
			t := &anAccumTool{id: b.ID, name: b.Name}
			t.input.Write(argsToJSON(b.Input))
			tools = append(tools, t)
		}
	}
	return withUsageA(a.final(sb.String(), tools), full.Usage), nil
}

// anyAnTool 报告任一内容块槽位是否包含工具调用。
func anyAnTool(tools []*anAccumTool) bool {
	for _, t := range tools {
		if t != nil {
			return true
		}
	}
	return false
}

func (a *anthropicLLM) final(text string, tools []*anAccumTool) *model.LLMResponse {
	var parts []*genai.Part
	var skipped []string
	for _, t := range tools {
		if t == nil {
			continue
		}
		args, err := argsToMap([]byte(t.input.String()))
		if err != nil {
			skipped = append(skipped, t.name)
			continue
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
			ID: t.id, Name: t.name, Args: args,
		}})
	}
	return finalLLMResponse(a.cfg.Model, appendBrokenArgsNote(text, skipped), parts)
}

type anAccumTool struct {
	id, name string
	input    strings.Builder
}

func argsToJSON(v any) []byte {
	b, _ := json.Marshal(v)
	if string(b) == "null" {
		return []byte("{}")
	}
	return b
}

func (a *anthropicLLM) post(ctx context.Context, body anRequest) (*http.Response, error) {
	payload, _ := json.Marshal(body)
	build := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+"/v1/messages", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		if a.cfg.APIKey != "" {
			httpReq.Header.Set("x-api-key", a.cfg.APIKey)
		}
		return httpReq, nil
	}
	resp, err := postWithRetry(ctx, build, a.cfg.Model, "anthropic")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("Anthropic %s: HTTP %d: %s", a.cfg.Model, resp.StatusCode, readErrBody(resp.Body))
	}
	return resp, nil
}

// maxProviderRetries 是用于瞬时供应商故障的内置重试预算。它有意不可由用户配置：五次重试可覆盖短暂
// 网络波动和网关重启，而无需设置 UI。
const maxProviderRetries = 5

// retryDelays 是瞬时故障重试之间的等待时长。
var retryDelays = []time.Duration{
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
}

// maxRetryAfter 限制服务器提供的 Retry-After
// 等待时间的上限：原样遵循过大的值会使重试停滞不合理长的时间。
const maxRetryAfter = 8 * time.Second

// retryAfterDelay 解析 Retry-After 头（秒数形式）；缺失或无法解析时为 0。
func retryAfterDelay(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs > 0 {
		d := time.Duration(secs) * time.Second
		if d > maxRetryAfter {
			return maxRetryAfter
		}
		return d
	}
	return 0
}

// postWithRetry 执行由 build 构建的请求，对瞬时失败（网络错误、429 限流以及
// 502/503/504）进行退避重试，最多额外重试五次。带 Retry-After 头的 429
// 至少等待该时长（有上限）。仅当尚未消费任何响应字节时才会重试，因此流式输出绝
// 不会重复。
func postWithRetry(ctx context.Context, build func() (*http.Request, error), model, kind string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := httpClient.Do(req)
		var delay time.Duration
		if attempt < len(retryDelays) {
			delay = retryDelays[attempt]
		}
		if err != nil {
			lastErr = err
		} else if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			if resp.StatusCode == http.StatusTooManyRequests {
				if ra := retryAfterDelay(resp.Header.Get("Retry-After")); ra > delay {
					delay = ra
				}
			}
			resp.Body.Close()
			lastErr = fmt.Errorf("%s %s: HTTP %d", kind, model, resp.StatusCode)
		} else {
			return resp, nil
		}
		if attempt >= len(retryDelays) {
			return nil, fmt.Errorf("%s: %w", kind, lastErr)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
}
