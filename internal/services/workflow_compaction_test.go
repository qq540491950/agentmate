package services

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/config"
)

// TestActualCompactionKeepsWorkflowAnchorsAndToolPairs 用真实 ADK 尾部压缩
// 证明：压缩事件已经发生，但下一次模型请求仍重新附加当前计划、未决审批和
// 最近副作用证据；压缩后的历史也不会留下孤立的工具调用/结果。
func TestActualCompactionKeepsWorkflowAnchorsAndToolPairs(t *testing.T) {
	var mu sync.Mutex
	streamTurn := 0
	var secondRequest string
	var sawSummaryCall bool
	chat, store := newTodosTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		if strings.Contains(body, `"stream":false`) {
			mu.Lock()
			sawSummaryCall = true
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"压缩摘要"},"finish_reason":"stop"}]}`))
			return
		}
		mu.Lock()
		streamTurn++
		current := streamTurn
		if current == 3 {
			secondRequest = body
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		switch current {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("plan_call", "plan_todos",
				`"{\"todos\":[{\"id\":\"step-1\",\"content\":\"起草周报\",\"status\":\"in_progress\",\"acceptance\":[\"写入 weekly.md\"]}]}"`) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseText("计划已保存。") + sseDone))
		case 3:
			_, _ = w.Write([]byte(sseText("从检查点继续完成。") + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("完成。") + sseDone))
		}
	})
	disabled := false
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.CompactionInterval = 1
		cfg.Settings.CompactionTokenThreshold = 0
		cfg.Settings.AutoTitle = &disabled
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "agent_t", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "先规划再执行", nil); err != nil {
		t.Fatal(err)
	}

	var turn workflowTurn
	if err := chat.db.Where("session_id = ?", sess.ID).Order("created_at DESC").First(&turn).Error; err != nil {
		t.Fatal(err)
	}
	pending := workflowApproval{
		ID: "pending_approval_call", TurnID: turn.ID, SessionID: sess.ID,
		ToolName: "write_workspace_file", Risk: "medium", Hint: "等待写入 weekly.md",
		Status: "pending", CreatedAt: time.Now().UTC(),
	}
	if err := chat.db.Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	evidence := toolInvocation{
		ID: "evidence_call", SessionID: sess.ID, TurnID: turn.ID,
		ToolName: "read_workspace_file", Status: "succeeded",
		BeforeHash: "before-evidence", AfterHash: "after-evidence",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := chat.db.Create(&evidence).Error; err != nil {
		t.Fatal(err)
	}

	if err := chat.db.Model(&workflowTurn{}).Where("id = ?", turn.ID).
		Updates(map[string]any{"status": turnStatusFailed, "finish_reason": "test_resume"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := chat.ResumeTurn(sess.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	summaryCalled := sawSummaryCall
	body := secondRequest
	mu.Unlock()
	if !summaryCalled {
		t.Fatal("expected an actual ADK compaction model call")
	}
	for _, want := range []string{
		"workflow_anchors", "起草周报", "pending_approval_call",
		"evidence_call", "read_workspace_file",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("model request after compaction missing %q", want)
		}
	}

	resp, err := chat.sessions.Get(t.Context(), &session.GetRequest{
		AppName: chat.appName, UserID: chat.userID, SessionID: sess.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]bool{}
	responses := map[string]bool{}
	var compacted bool
	for ev := range resp.Session.Events().All() {
		if ev == nil {
			continue
		}
		if ev.Actions.Compaction != nil {
			compacted = true
		}
		if ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil {
				calls[part.FunctionCall.ID] = true
			}
			if part.FunctionResponse != nil {
				responses[part.FunctionResponse.ID] = true
			}
		}
	}
	if !compacted {
		t.Fatal("expected a persisted ADK compaction event")
	}
	for id := range calls {
		if !responses[id] {
			t.Fatalf("compaction left an orphan function call %q", id)
		}
	}
	for id := range responses {
		if !calls[id] {
			t.Fatalf("compaction left an orphan function response %q", id)
		}
	}
}
