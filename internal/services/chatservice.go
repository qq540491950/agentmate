package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
	"gorm.io/gorm"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
)

// EventChatStream 是发送到前端的事件名称（在 main.go 中注册）。
const (
	EventChatStream = "chat:stream"
)

// appEventHook 在设置后（测试中）接收流式事件，而不是通过 Wails 事件总线。
var appEventHook func(ChatStreamEvent)

// AttachmentIn 是用户附加到消息上的图片（base64 数据）。
type AttachmentIn struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data string `json:"data"` // base64，不带 data: 前缀
}

// AttachmentOut 是聊天历史中渲染的附件。
type AttachmentOut struct {
	Name    string `json:"name"`
	MIME    string `json:"mime"`
	DataURL string `json:"dataUrl"`
}

// UsageInfo 报告一个回合或整个会话的 token 消耗。 ContextTokens
// 是当前上下文占用：最后一次模型调用的提示词 token 数（addUsage
// 在每次调用时都会覆盖它，因此它绝不会累加）。有意不采用累计总量——每次调用都会
// 重新发送完整历史，因此一次工具循环中各次调用的总和会是真实上下文大小的倍数（
// 计费语义与窗口语义）。
type UsageInfo struct {
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	ContextTokens    int64 `json:"contextTokens,omitempty"`
	// CachedTokens 是最后一次提示词中由供应商从其提示词缓存提供的部分（Anthropic
	// cache_read_input_tokens、OpenAI 风格的
	// prompt_tokens_details.cached_tokens）。零表示 "no caching
	// reported"，绝不表示 "cache miss"——若干网关只是省略该字段。
	CachedTokens int64 `json:"cachedTokens,omitempty"`
}

// ChatStreamEvent 在一次运行进行期间流式发送到前端。
type ChatStreamEvent struct {
	SessionID string         `json:"sessionId"`
	Kind      string         `json:"kind"` // user|delta|thinking_delta|thinking|message|tool_call|tool_result|confirm_request|done|error
	Author    string         `json:"author"`
	Text      string         `json:"text"`
	ToolName  string         `json:"toolName,omitempty"`
	ToolArgs  map[string]any `json:"toolArgs,omitempty"`
	ToolResp  map[string]any `json:"toolResp,omitempty"`
	// CallID 标识此事件所指的 ADK 调用：tool_call / tool_result
	// 对，或一次确认中断。确认答复通过 ConfirmToolCall 回传。
	CallID string `json:"callId,omitempty"`
	// Hint 是模型提供的说明，显示在确认卡片上。
	Hint string `json:"hint,omitempty"`
	// Usage 在回合的最后一条助手消息事件上设置。
	Usage *UsageInfo `json:"usage,omitempty"`
	// Attachments 随用户事件携带，使刚发送的消息（以及重新生成的消息）无需等待历史记录重新加载
	// 即可渲染其图片。
	Attachments []AttachmentOut `json:"attachments,omitempty"`
	// Reasoning 是应用于本回合的思考级别（当模型未提供表，或该级别留由供应商默认值决定时为空
	// ）。在用户事件上设置，以便气泡能显示其发送时所带的级别。
	Reasoning string `json:"reasoning,omitempty"`
	// SessionUsage 是会话的累计用量，在 done 事件上设置。
	SessionUsage *UsageInfo `json:"sessionUsage,omitempty"`
}

// usageOf 从 ADK 事件中提取 token 用量（如果存在）。
func usageOf(ev *session.Event) *UsageInfo {
	u := ev.LLMResponse.UsageMetadata
	if u == nil {
		return nil
	}
	return &UsageInfo{
		PromptTokens:     int64(u.PromptTokenCount),
		CompletionTokens: int64(u.CandidatesTokenCount),
		TotalTokens:      int64(u.TotalTokenCount),
		ContextTokens:    int64(u.PromptTokenCount),
		CachedTokens:     int64(u.CachedContentTokenCount),
	}
}

// ChatMessage 是渲染后的历史消息。
type ChatMessage struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"` // user|assistant|thinking|tool_call|tool_result|confirm_request|confirm_result
	Author      string          `json:"author"`
	Text        string          `json:"text"`
	ToolName    string          `json:"toolName,omitempty"`
	ToolArgs    map[string]any  `json:"toolArgs,omitempty"`
	ToolResp    map[string]any  `json:"toolResp,omitempty"`
	CallID      string          `json:"callId,omitempty"`
	Hint        string          `json:"hint,omitempty"`
	Attachments []AttachmentOut `json:"attachments,omitempty"`
	// Reasoning 是这一回合发送时使用的思考级别；记录在用户消息的状态增量上，以便历史记录和导
	// 出可以显示它。
	Reasoning string    `json:"reasoning,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// turnReasoningStateKey 在用户消息事件上记录一个回合的思考级别。该键刻意不带
// "temp:"
// 前缀：数据库会话服务会从已存储的事件中剥离临时键，而历史渲染在重新加载后需要
// 该值。
const turnReasoningStateKey = "agentmate.reasoning_level"

const (
	maxAttachmentBytes = 6 << 20 // 每个文件解码后 6 MB
	maxAttachments     = 4
)

// directAssistantName 是隐式直聊 Agent 的显示名称。
const directAssistantName = "助手"

var attachmentMIMEAllowed = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
}

// displayAuthor 将 ADK 事件作者（ADK
// 运行所用的净化标识符）映射回配置的显示名称。非 ASCII 的 Agent
// 名称始终会净化为 agent_<hash> 回退名称——没有此映射，聊天 UI
// 会在每个气泡上显示该哈希。未知作者原样透传。
func displayAuthor(cfg config.Config, author string) string {
	if author == "" || author == "user" {
		return author
	}
	for _, a := range cfg.Agents {
		if a.Name != "" && agentkit.SanitizeAgentName(a.Name) == author {
			return a.Name
		}
	}
	for _, t := range cfg.Teams {
		if t.Name != "" && agentkit.SanitizeAgentName(t.Name) == author {
			return t.Name
		}
	}
	if author == agentkit.SanitizeAgentName(directAssistantName) {
		return directAssistantName
	}
	return author
}

// buildUserContent 组装用户消息内容：文本加图像部分。
func buildUserContent(text string, atts []AttachmentIn) (*genai.Content, error) {
	content := genai.NewContentFromText(text, genai.RoleUser)
	for i, a := range atts {
		if !attachmentMIMEAllowed[a.MIME] {
			return nil, fmt.Errorf("不支持的附件类型 %s（仅支持图片）", a.MIME)
		}
		raw, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil {
			return nil, fmt.Errorf("附件 %s 解码失败", a.Name)
		}
		if len(raw) > maxAttachmentBytes {
			return nil, fmt.Errorf("附件 %s 超过 6MB 限制", a.Name)
		}
		if i >= maxAttachments {
			return nil, fmt.Errorf("最多附带 %d 个附件", maxAttachments)
		}
		content.Parts = append(content.Parts, &genai.Part{
			InlineData: &genai.Blob{MIMEType: a.MIME, Data: raw},
		})
	}
	return content, nil
}

// attachmentsOut 将用户消息中的内联图片转换为聊天历史和流式事件所用的渲染形式。
func attachmentsOut(content *genai.Content) []AttachmentOut {
	if content == nil {
		return nil
	}
	var out []AttachmentOut
	for _, p := range content.Parts {
		if p != nil && p.InlineData != nil && len(p.InlineData.Data) > 0 {
			out = append(out, AttachmentOut{
				Name:    "attachment-" + strconv.Itoa(len(out)+1),
				MIME:    p.InlineData.MIMEType,
				DataURL: "data:" + p.InlineData.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(p.InlineData.Data),
			})
		}
	}
	return out
}

// ChatService 管理基于 ADK 的聊天会话和流式运行。
type ChatService struct {
	S *Services

	mu   sync.Mutex
	runs map[string]*runHandle
	// active 跟踪每个会话中的每一个在途句柄。runs
	// 仅保存当前句柄（用于取消）；重叠的较旧运行可能仍在收尾，修改历史的入口点也必
	// 须等待它。
	active    map[string]map[*runHandle]struct{}
	sessions  session.Service
	db        *gorm.DB // 用于事件裁剪的共享 SQLite 句柄（Regenerate）
	appName   string
	userID    string
	auditPath string // JSONL 工具审计轨迹；为空 = 禁用
	auditMu   sync.Mutex
	// workspaceMu 串行化任务工作区的分配/重新绑定，使两个并发创建不能都占用同一物理目录。
	workspaceMu sync.Mutex
	// workspaceLocks 串行化同一会话的回合与工作区重新绑定；不同任务仍可并发运行和创建。
	workspaceLocks map[string]*sync.Mutex
	// terminalMu：sessionTerminals 冻结会话启动时所用的
	// shell。它有意仅存于内存：应用重启后恢复的会话会采用当前的全局设置。
	terminalMu       sync.Mutex
	sessionTerminals map[string]config.TerminalSettings
}

// runHandle 标识一个进行中的回合。指针身份使一个正在结束的运行仅移除它自己的注册；普通的
// map delete 会在回合重叠时丢掉较新运行的 cancel func。
type runHandle struct {
	cancel context.CancelFunc
	// done 在运行完全结束（不再有事件写入）后由 release
	// 关闭；修改历史的入口点会等待它。
	done chan struct{}
}

// SetToolAuditPath 在 path 处启用 JSONL 工具调用审计追踪。
func (c *ChatService) SetToolAuditPath(path string) { c.auditPath = path }

// NewChatService 创建服务。db 可以为 nil（禁用 Regenerate 裁剪）。
func NewChatService(s *Services, sessions session.Service, db *gorm.DB) *ChatService {
	return &ChatService{
		S:                s,
		runs:             map[string]*runHandle{},
		active:           map[string]map[*runHandle]struct{}{},
		workspaceLocks:   map[string]*sync.Mutex{},
		sessionTerminals: map[string]config.TerminalSettings{},
		sessions:         sessions,
		db:               db,
		appName:          "agentmate",
		userID:           "local",
	}
}

func (c *ChatService) rememberSessionTerminal(sessionID string, settings config.TerminalSettings) {
	if sessionID == "" {
		return
	}
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	if c.sessionTerminals == nil {
		c.sessionTerminals = map[string]config.TerminalSettings{}
	}
	c.sessionTerminals[sessionID] = config.NormalizeTerminalSettings(settings)
}

func (c *ChatService) terminalSettingsFor(sessionID string, settings config.TerminalSettings) config.TerminalSettings {
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	if terminal, ok := c.sessionTerminals[sessionID]; ok {
		return terminal
	}
	if c.sessionTerminals == nil {
		c.sessionTerminals = map[string]config.TerminalSettings{}
	}
	terminal := config.NormalizeTerminalSettings(settings)
	c.sessionTerminals[sessionID] = terminal
	return terminal
}

func (c *ChatService) forgetSessionTerminal(sessionID string) {
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	delete(c.sessionTerminals, sessionID)
}

// auditMaxBytes 限制审计文件在轮转前的大小（10MB）。
const auditMaxBytes = 10 << 20

// audit 为工具审计轨迹追加一行 JSONL。达到 auditMaxBytes 时，文件轮转到
// <path>.old。失败会被静默忽略：审计绝不能中断对话。由互斥锁保护——并行会话
// 会并发轮转和追加。
func (c *ChatService) audit(sessionID, phase, author, tool string, payload []byte, errMsg string) {
	if c.auditPath == "" || !c.S.Store.Get().Settings.ToolsAudit {
		return
	}
	c.auditMu.Lock()
	defer c.auditMu.Unlock()
	rec, _ := json.Marshal(map[string]any{
		"ts": nowUTC(), "sessionId": sessionID, "phase": phase,
		"author": author, "tool": tool, "error": errMsg,
		"payload": json.RawMessage(payload),
	})
	if st, err := os.Stat(c.auditPath); err == nil && st.Size()+int64(len(rec))+1 >= auditMaxBytes {
		_ = os.Rename(c.auditPath, c.auditPath+".old")
	}
	f, err := os.OpenFile(c.auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(rec, '\n'))
}

// resultErrorOf 从工具响应载荷中提取 error 字段。
func resultErrorOf(resp map[string]any) string {
	if resp == nil {
		return ""
	}
	if e, ok := resp["error"].(string); ok {
		return e
	}
	return ""
}

func sessionUsageTotal(u *UsageInfo) int64 {
	if u == nil {
		return 0
	}
	return u.TotalTokens
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// recoverRun 将 agent/tool 栈中的 panic 转换为流式错误以及一个 done 事件，使 UI
// 始终离开运行状态。作为 runTurn 的延迟辅助函数使用。
func (c *ChatService) recoverRun(sessionID string, err *error) {
	r := recover()
	if r == nil {
		return
	}
	c.emitChat(sessionID, ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("运行出错：%v", r)})
	c.emitChat(sessionID, ChatStreamEvent{Kind: "done"})
	*err = fmt.Errorf("run turn panicked: %v", r)
}

// Cancel 中止会话当前正在进行的运行（如果有）。
func (c *ChatService) Cancel(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.runs[sessionID]; ok {
		h.cancel()
	}
}

// IsRunning 报告会话当前是否有正在进行的运行。
func (c *ChatService) IsRunning(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runs[sessionID] != nil
}

// registerRun 取消该会话的任何先前运行，并记录新的句柄。运行结束时，必须调用（延迟调用）返
// 回的 release。
func (c *ChatService) registerRun(sessionID string, cancel context.CancelFunc) (release func()) {
	h := &runHandle{cancel: cancel, done: make(chan struct{})}
	c.mu.Lock()
	if c.runs == nil {
		c.runs = map[string]*runHandle{}
	}
	if c.active == nil {
		c.active = map[string]map[*runHandle]struct{}{}
	}
	old := c.runs[sessionID]
	c.runs[sessionID] = h
	set := c.active[sessionID]
	if set == nil {
		set = map[*runHandle]struct{}{}
		c.active[sessionID] = set
	}
	set[h] = struct{}{}
	c.mu.Unlock()
	if old != nil {
		old.cancel()
	}
	return func() {
		c.mu.Lock()
		// 仅注销我们自己的句柄：更新的回合可能已经占用了该槽位。
		if c.runs[sessionID] == h {
			delete(c.runs, sessionID)
		}
		if set := c.active[sessionID]; set != nil {
			delete(set, h)
			if len(set) == 0 {
				delete(c.active, sessionID)
			}
		}
		c.mu.Unlock()
		close(h.done)
	}
}

// cancelAndWaitBounded 中止会话的所有正在进行中的运行（当前运行以及任何仍在收尾的重叠旧句柄），并在
// 有界时间内等待，直到它们全部完全收尾。Regenerate / EditAndResend /
// DeleteSession 在此返回后立即裁剪或删除已存储的事件；否则，仍在流式输出的运行可能会使已删除
// 的历史重新出现。
func (c *ChatService) cancelAndWaitBounded(sessionID string) {
	c.mu.Lock()
	var handles []*runHandle
	seen := map[*runHandle]bool{}
	for h := range c.active[sessionID] {
		handles = append(handles, h)
		seen[h] = true
	}
	// 防御性：在 active 存在之前注册的句柄（测试）仍会门控裁剪。
	if h := c.runs[sessionID]; h != nil && !seen[h] {
		handles = append(handles, h)
	}
	c.mu.Unlock()
	if len(handles) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, h := range handles {
		h.cancel()
	}
	for _, h := range handles {
		select {
		case <-h.done:
		case <-ctx.Done():
			// 有界：卡住的收尾不得让 RPC 永久挂起；掉队者的写入不会比修复前的行为更糟。
			return
		}
	}
}
