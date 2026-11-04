// Package agentkit builds ADK agents from user configuration.
// This file implements model.LLM adapters for OpenAI-compatible
// Chat Completions APIs and the Anthropic Messages API, both with
// streaming and function calling, so any provider can be configured.
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

// httpClient is shared by both adapters. Streaming responses are bounded by
// the request context, never by a client-wide timeout. An empty client is
// intentional: it rides http.DefaultTransport so ApplyGlobalProxy applies.
var httpClient = &http.Client{}

func readErrBody(body io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(body, 4096))
	return strings.TrimSpace(string(b))
}

// ---------------------------------------------------------------------------
// genai helpers shared by both adapters

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

// functionParameters resolves a declaration's JSON Schema. ADK's built-in
// tools (functiontool) and MCP toolsets publish their schema through
// ParametersJsonSchema; reading only the legacy genai.Schema field advertised
// every tool to OpenAI-compatible/Anthropic providers as an argument-less
// {"type":"object"}, so models could not call workspace, skill or MCP tools
// with the right arguments (issue #23).
func functionParameters(fd *genai.FunctionDeclaration) map[string]any {
	if fd.ParametersJsonSchema != nil {
		if m, ok := jsonSchemaMap(fd.ParametersJsonSchema); ok {
			return m
		}
	}
	return schemaToJSONSchema(fd.Parameters)
}

// jsonSchemaMap normalizes a JSON-schema payload (jsonschema-go structs, the
// map[string]any MCP toolsets use, or any other JSON-marshalable value) into a
// plain object.
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

// stopSequencesOf surfaces the configured stop sequences (nil when unset).
func stopSequencesOf(req *model.LLMRequest) []string {
	if req.Config == nil {
		return nil
	}
	return req.Config.StopSequences
}

// withUsage attaches provider usage to a final response so the ADK runner
// stores it and the UI can surface token counts.
func withUsage(resp *model.LLMResponse, u *oaUsage) *model.LLMResponse {
	if resp == nil || u == nil {
		return resp
	}
	resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     int32(u.PromptTokens),
		CandidatesTokenCount: int32(u.CompletionTokens),
		TotalTokenCount:      int32(u.TotalTokens),
	}
	return resp
}

func withUsageA(resp *model.LLMResponse, u *anUsage) *model.LLMResponse {
	if resp == nil || u == nil {
		return resp
	}
	total := u.InputTokens + u.OutputTokens
	resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     int32(u.InputTokens),
		CandidatesTokenCount: int32(u.OutputTokens),
		TotalTokenCount:      int32(total),
	}
	return resp
}

// argsToMap parses tool-call arguments. An error means the streamed argument
// fragments never formed valid JSON (truncated stream, dropped lines); callers
// must not invoke the tool with zero-value arguments — silently running a
// write tool with empty args is worse than failing the turn loudly.
func argsToMap(raw []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// appendBrokenArgsNote appends a user-visible note for tool calls whose
// streamed arguments never formed valid JSON (they were skipped, not run).
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

// finalLLMResponse assembles the single non-partial event that the ADK
// Runner processes: complete text plus every function call.
func finalLLMResponse(modelName, text string, parts []*genai.Part) *model.LLMResponse {
	if text != "" || len(parts) == 0 {
		parts = append([]*genai.Part{{Text: text}}, parts...)
	}
	return &model.LLMResponse{
		ModelVersion: modelName,
		Content:      &genai.Content{Role: genai.RoleModel, Parts: parts},
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

func scanSSE(body io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	return sc
}

// ---------------------------------------------------------------------------
// Streaming response plumbing shared by both adapters

// streamIdleTimeout bounds how long an open stream may stay silent. Without
// it a provider/gateway that stopped sending bytes but never closed the
// response left the turn in "正在回复…" until the whole-turn deadline (10
// minutes by default) — the symptom reported in issue #23.
var streamIdleTimeout = 2 * time.Minute

// startStreamWatchdog turns the request context into one that cancels after
// `d` without activity. Callers reset the timer for every received line, so
// only a genuinely stalled stream trips it. stop must run when the stream
// ends; it does not turn the normal completion path into a failure.
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

// streamFailure turns a transport error into the most useful message: the
// caller's own cancellation wins, then a stream watchdog trip, then the raw
// error.
func streamFailure(parent context.Context, idle *atomic.Bool, fallback error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if idle != nil && idle.Load() {
		return fmt.Errorf("模型供应商超过 %s 没有返回任何数据，已中断本轮（请检查网络或代理后重试）", streamIdleTimeout)
	}
	return fallback
}

// bodyKind classifies the start of a model response body.
type bodyKind int

const (
	bodySSE bodyKind = iota
	bodyJSON
)

// sniffBody reports how a response body starts and replays every byte it
// consumed. A plain JSON document (some gateways answer a stream request with
// a single JSON object, often an error when their upstream died) is decoded as
// a whole; everything else is scanned as SSE, where unrecognised lines are
// skipped as noise. Treating a JSON error object as SSE silently ended the turn
// with no content.
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
			// EOF (empty body) or an unreasonable run of blank lines: stop
			// sniffing and let the SSE scanner deal with the rest.
			return bodySSE, io.MultiReader(bytes.NewReader(prefix), br), err
		}
	}
}

// bodySnippet returns a short, printable excerpt of an unexpected response
// body for the error message.
func bodySnippet(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// OpenAI Chat Completions compatible adapter

// OpenAICompatConfig configures the OpenAI-compatible adapter.
type OpenAICompatConfig struct {
	BaseURL string // e.g. https://api.deepseek.com/v1
	APIKey  string
	Model   string
	// ExtraBody carries the resolved model-level thinking fields
	// (reasoning_effort, thinking, ...). They are merged into every request.
	ExtraBody map[string]any
}

type openaiCompatLLM struct {
	cfg OpenAICompatConfig
}

// NewOpenAICompat builds a model.LLM speaking the OpenAI Chat Completions
// protocol (OpenAI, DeepSeek, Kimi, Qwen, Ollama, vLLM, ...).
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
	Content    any          `json:"content,omitempty"` // string or content-part array
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
	// ExtraBody is merged into the JSON object after the typed fields.
	ExtraBody map[string]any `json:"-"`
}

// MarshalJSON keeps the typed request shape and merges ExtraBody (thinking
// levels) at the top level.
func (r oaRequest) MarshalJSON() ([]byte, error) {
	type alias oaRequest
	return marshalWithExtra(alias(r), r.ExtraBody)
}

type oaUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
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

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content   string       `json:"content"`
			ToolCalls []oaToolCall `json:"tool_calls"`
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
			case p.Text != "":
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
			// Some gateways ignore "stream":true and answer with one plain
			// document (often an error object when their upstream died).
			// Parsing it keeps the failure visible instead of ending the turn
			// with no content.
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
		calls := map[int]*oaAccumCall{}
		var order []int
		// Leading non-SSE lines are skipped as noise (some gateways prepend
		// status text), but remembering the first one lets an HTML/plain-text
		// failure page surface as an error instead of an empty answer.
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
				// [DONE] is the protocol terminator. Waiting for EOF instead
				// hung the turn whenever a gateway/proxy kept the response open
				// after the last chunk (issue #23).
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
			if len(chunk.Choices) == 0 {
				if chunk.Usage != nil {
					usage = chunk.Usage
				}
				continue
			}
			d := chunk.Choices[0].Delta
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
		yield(withUsage(o.final(sb.String(), calls, order), usage), nil)
	}
}

// oaFullResponse is a complete (non-SSE) Chat Completions response.
type oaFullResponse struct {
	Choices []struct {
		Message struct {
			Content   string       `json:"content"`
			ToolCalls []oaToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *oaUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// fullResponse decodes a complete chat completion, surfacing a provider error
// object and refusing an answer-less payload instead of silently ending the
// turn.
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
	calls := map[int]*oaAccumCall{}
	var order []int
	sb.WriteString(full.Choices[0].Message.Content)
	for i, tc := range full.Choices[0].Message.ToolCalls {
		// Complete responses carry no per-call index; the slice position is
		// the only ordering. Using tc.Index merged every call into index 0.
		oaAccumulate(calls, &order, i, tc.ID, tc.Function.Name, tc.Function.Arguments)
	}
	return withUsage(o.final(sb.String(), calls, order), full.Usage), nil
}

// oaAccumulate groups streamed tool-call fragments by index.
func oaAccumulate(calls map[int]*oaAccumCall, order *[]int, idx int, id, name, argDelta string) *oaAccumCall {
	if idx < 0 || idx > 1024 {
		return nil // broken/harsh provider index; ignore the fragment
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

func (o *openaiCompatLLM) final(text string, calls map[int]*oaAccumCall, order []int) *model.LLMResponse {
	var parts []*genai.Part
	var skipped []string
	for _, idx := range order {
		c := calls[idx]
		args, err := argsToMap([]byte(c.args.String()))
		if err != nil {
			// Broken argument stream: executing the tool with {} would be a
			// silent zero-value call. Surface it in the reply text instead.
			skipped = append(skipped, c.name)
			continue
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: c.id, Name: c.name, Args: args}})
	}
	return finalLLMResponse(o.cfg.Model, appendBrokenArgsNote(text, skipped), parts)
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
// Anthropic Messages adapter

// AnthropicConfig configures the Anthropic adapter.
type AnthropicConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	// ExtraBody carries the resolved model-level thinking fields (thinking,
	// output_config, ...). They are merged into every request.
	ExtraBody map[string]any
}

type anthropicLLM struct {
	cfg AnthropicConfig
}

// NewAnthropic builds a model.LLM speaking the Anthropic Messages API.
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
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	// image
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
	// StopSequences maps the genai config onto Anthropic's stop_sequences.
	StopSequences []string `json:"stop_sequences,omitempty"`
	// ExtraBody is merged into the JSON object after the typed fields.
	ExtraBody map[string]any `json:"-"`
}

// MarshalJSON keeps the typed request shape and merges ExtraBody (thinking
// levels) at the top level.
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
			case p.Text != "":
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
		// Anthropic indexes content blocks; a hostile/broken endpoint can send
		// negative or absurd indexes (panic / OOM via unbounded append).
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
				// The protocol terminator. Waiting for EOF instead hung the
				// turn whenever the gateway kept the connection open after the
				// final event (issue #23).
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
					usage = &anUsage{InputTokens: ev.Message.Usage.InputTokens}
				}
			case "message_delta":
				if ev.Usage == nil {
					break
				}
				// Some proxies drop message_start; synthesize the accumulator
				// here so output tokens are still counted.
				if usage == nil {
					usage = &anUsage{}
				}
				usage.OutputTokens = ev.Usage.OutputTokens
			case "content_block_start":
				if ev.ContentBlock.Type == "tool_use" {
					if ev.Index < 0 || ev.Index >= maxContentBlocks {
						continue
					}
					getTool(ev.Index) // reserve slot
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

// anFullResponse is a complete (non-SSE) Anthropic Messages response.
type anFullResponse struct {
	Content []anBlock `json:"content"`
	Usage   *anUsage  `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// fullResponse decodes a complete Messages response. Non-stream responses (the
// A2A executor path) carry usage too — without it the server-side token
// accounting was empty.
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

// anyAnTool reports whether any content-block slot holds a tool call.
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

// retryDelays are the fixed waits between transient-failure retries.
var retryDelays = []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond}

// maxRetryAfter caps a server-provided Retry-After wait: honouring a huge
// value verbatim would eat the whole turn timeout budget.
const maxRetryAfter = 8 * time.Second

// retryAfterDelay parses a Retry-After header (seconds form); 0 when absent
// or unparseable.
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

// postWithRetry executes the request built by build, retrying transient
// failures (network errors, 429 rate limiting and 502/503/504) up to
// len(retryDelays) extra times with backoff. A 429 with a Retry-After header
// waits at least that long (capped). Retries only happen when no response
// bytes have been consumed, so streaming output is never duplicated.
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
