package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

var NewOpenAICompat = agentkit.NewOpenAICompat

func newTestRequest(text string) *model.LLMRequest {
	return &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText(text, genai.RoleUser)}}
}

func newTestServices(store *config.Store) *Services {
	return &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
}

// TestRunTurnPanicBecomesErrorEvent: agent/tool 栈中的 panic
// 不得导致进程崩溃；它会以错误事件加 done 的形式暴露，以便 UI 退出运行状态。
func TestRunTurnPanicBecomesErrorEvent(t *testing.T) {
	var events []ChatStreamEvent
	origApp := appEventHook
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = origApp })

	chat := &ChatService{runs: map[string]*runHandle{}}
	var err error
	func() {
		defer chat.recoverRun("s1", &err)
		panic("tool exploded")
	}()

	if err == nil || !strings.Contains(err.Error(), "tool exploded") {
		t.Fatalf("panic not converted to error: %v", err)
	}
	var sawError, sawDone bool
	for _, ev := range events {
		if ev.Kind == "error" && strings.Contains(ev.Text, "tool exploded") {
			sawError = true
		}
		if ev.Kind == "done" {
			sawDone = true
		}
	}
	if !sawError || !sawDone {
		t.Fatalf("panic recovery did not emit error+done: %+v", events)
	}

	// 没有 panic => 没有事件，错误保持不变。
	events = nil
	var clean error
	func() {
		defer chat.recoverRun("s1", &clean)
	}()
	if clean != nil || len(events) != 0 {
		t.Fatalf("recoverRun touched a clean run: err=%v events=%+v", clean, events)
	}
}

// TestRunHandleUnregistersOnlyOwnEntry 锁定重叠场景：当回合 B 在回合 A
// 尚未完成收尾前启动时，A 的清理不得移除 B 的取消注册项（否则会使 Cancel
// 成为空操作，并让仍在流式传输的运行被 IsRunning 报告为 false）。
func TestRunHandleUnregistersOnlyOwnEntry(t *testing.T) {
	chat := &ChatService{runs: map[string]*runHandle{}}

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	releaseA := chat.registerRun("s1", cancelA)

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	releaseB := chat.registerRun("s1", cancelB)

	// 启动 B 会取消 A（每个会话仅一个回合）。
	<-ctxA.Done()

	releaseA() // A 在 B 注册后完成收尾
	if !chat.IsRunning("s1") {
		t.Fatal("finishing the old run deregistered the new one")
	}
	chat.Cancel("s1")
	select {
	case <-ctxB.Done():
	default:
		t.Fatal("Cancel did not reach the live run")
	}

	releaseB()
	if chat.IsRunning("s1") {
		t.Fatal("run stayed registered after release")
	}
	_ = ctxB
}

// TestCancelAndWaitWaitsForEveryHandle: 当回合 B 在注册表中替换 A 时，A
// 可能仍在收尾。会修改历史的入口必须等待两者——仅等待当前句柄会让 A
// 剩余的写入使已裁剪的历史重新出现。
func TestCancelAndWaitWaitsForEveryHandle(t *testing.T) {
	chat := &ChatService{runs: map[string]*runHandle{}, active: map[string]map[*runHandle]struct{}{}}

	_, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	releaseA := chat.registerRun("s1", cancelA)
	_, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	releaseB := chat.registerRun("s1", cancelB) // 启动 B 会取消 A

	done := make(chan struct{})
	go func() {
		chat.cancelAndWaitBounded("s1")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("returned before any handle unwound")
	case <-time.After(50 * time.Millisecond):
	}
	releaseB()
	select {
	case <-done:
		t.Fatal("returned while the older overlapping handle was still unwinding")
	case <-time.After(50 * time.Millisecond):
	}
	releaseA()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not return after every handle was released")
	}
}

// TestStoreConcurrentUpdates 从多个 goroutine 反复冲击 config
// 存储，以验证互斥锁保持状态一致（无竞态，无 schema 丢失）。
func TestStoreConcurrentUpdates(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = store.Update(func(cfg *config.Config) {
				cfg.Agents = append(cfg.Agents[:0], cfg.Agents...)
				cfg.Providers = append(cfg.Providers, config.ModelProvider{
					ID: "p", Name: "x", Protocol: config.ProtocolOpenAI,
				})
			})
		}(i)
	}
	wg.Wait()
	if got := len(store.Get().Providers); got != 32 {
		t.Fatalf("lost updates: got %d providers, want 32", got)
	}
}

// TestAdapterMalformedSSE 向两个适配器输入垃圾行、损坏的 JSON
// 和超大负载：它们必须跳过噪声，并在不 panic 的情况下仍生成最终响应。
func TestAdapterMalformedSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("not-an-event\n\n"))
		_, _ = w.Write([]byte("data: not-json{{{\n\n"))
		_, _ = w.Write([]byte("data: []\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: " + strings.Repeat("x", 300000) + "\n\n")) // 超长行
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	llm, err := agentkit.NewOpenAICompat(agentkit.OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	req := newTestRequest("hi")
	var text strings.Builder
	var usageSeen bool
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("malformed SSE produced error: %v", err)
		}
		if resp == nil {
			continue
		}
		if resp.Partial && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				text.WriteString(p.Text)
			}
		}
		if !resp.Partial {
			usageSeen = resp.UsageMetadata != nil && resp.UsageMetadata.TotalTokenCount == 2
		}
	}
	if text.String() != "ok" {
		t.Fatalf("text = %q", text.String())
	}
	if !usageSeen {
		t.Fatal("usage chunk lost among malformed lines")
	}
}

// TestSendCancelIsStable 验证在慢速运行期间取消能够干净地终止，且不会使 cancels
// map 死锁。
func TestSendCancelIsStable(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	defer close(block)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Slow", Protocol: config.ProtocolOpenAI, BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	})
	db, sessions, err := newTestSessionService(t, dir+"/s.db")
	if err != nil {
		t.Fatal(err)
	}
	svc := newTestServices(store)
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_ = chat.Send(sess.ID, "slow", nil)
		close(done)
	}()
	for !chat.IsRunning(sess.ID) {
		select {
		case <-done:
			t.Fatal("send finished before cancel")
		default:
		}
	}
	chat.Cancel(sess.ID)
	<-done
	if chat.IsRunning(sess.ID) {
		t.Fatal("cancel left the run registered")
	}
	// 之后会话仍可使用。
	if _, err := chat.GetSessionMessages(sess.ID); err != nil {
		t.Fatalf("session unusable after cancel: %v", err)
	}
}
