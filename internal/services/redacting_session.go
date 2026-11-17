package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/agentkit"
)

// redactingSessionService 在事件落盘前脱敏文本、工具参数与工具结果。
// 它是 ADK session.Service 的装饰器：读取路径保持原样，写入路径统一收口，
// 因此模型后续看到的历史也不会再次携带已识别的密钥。
type redactingSessionService struct {
	session.Service
	artifactDir  string
	budgetMu     sync.Mutex
	sessionBytes map[string]int
}

const (
	maxInlineToolResponseBytes   = 64 * 1024
	maxToolResponseBudgetPerTurn = 128 * 1024
)

func newRedactingSessionService(inner session.Service, artifactDir string) session.Service {
	if inner == nil {
		return nil
	}
	return &redactingSessionService{
		Service: inner, artifactDir: artifactDir, sessionBytes: map[string]int{},
	}
}

func (r *redactingSessionService) AppendEvent(ctx context.Context, sess session.Session, ev *session.Event) error {
	redactEvent(ev)
	r.spillLargeToolResponses(sess, ev)
	return r.Service.AppendEvent(ctx, sess, ev)
}

func redactEvent(ev *session.Event) {
	if ev == nil {
		return
	}
	if ev.Content != nil {
		for _, part := range ev.Content.Parts {
			if part == nil {
				continue
			}
			part.Text = agentkit.SanitizeSecrets(part.Text)
			if part.FunctionCall != nil {
				part.FunctionCall.Args = redactMap(part.FunctionCall.Args)
			}
			if part.FunctionResponse != nil {
				part.FunctionResponse.Response = redactMap(part.FunctionResponse.Response)
			}
		}
	}
	if ev.Actions.StateDelta != nil {
		ev.Actions.StateDelta = redactMap(ev.Actions.StateDelta)
	}
}

func (r *redactingSessionService) spillLargeToolResponses(sess session.Session, ev *session.Event) {
	if ev == nil || ev.Content == nil {
		return
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID()
	}
	r.budgetMu.Lock()
	defer r.budgetMu.Unlock()
	if ev.Author == "user" && !hasFunctionResponse(ev) {
		delete(r.sessionBytes, sessionID)
	}
	for _, part := range ev.Content.Parts {
		if part == nil || part.FunctionResponse == nil {
			continue
		}
		data, err := json.Marshal(part.FunctionResponse.Response)
		if err != nil {
			continue
		}
		used := r.sessionBytes[sessionID]
		if len(data) <= maxInlineToolResponseBytes && used+len(data) <= maxToolResponseBudgetPerTurn {
			r.sessionBytes[sessionID] = used + len(data)
			continue
		}
		r.sessionBytes[sessionID] = used + len(data)
		path := r.writeToolArtifact(data)
		part.FunctionResponse.Response = map[string]any{
			"ok":             true,
			"truncated":      true,
			"bytes":          len(data),
			"artifact":       path,
			"summary":        summarizeToolResponse(data),
			"note":           "完整工具结果已落盘；如需细节请读取 artifact 或缩小查询范围。",
			"untrusted_data": true,
		}
	}
}

func (r *redactingSessionService) writeToolArtifact(data []byte) string {
	if r.artifactDir == "" {
		return ""
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:12]) + ".json"
	if err := os.MkdirAll(r.artifactDir, 0o755); err != nil {
		return ""
	}
	full := filepath.Join(r.artifactDir, name)
	if err := os.WriteFile(full, data, 0o600); err != nil {
		return ""
	}
	return full
}

func summarizeToolResponse(data []byte) string {
	text := string(data)
	if len(text) > 1200 {
		text = text[:1200] + "…"
	}
	return strings.TrimSpace(text)
}

func redactMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	for key, value := range in {
		in[key] = redactValue(value)
	}
	return in
}

func redactMapCopy(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return redactMap(out)
}

func redactValue(value any) any {
	switch v := value.(type) {
	case string:
		return agentkit.SanitizeSecrets(v)
	case map[string]any:
		return redactMap(v)
	case []any:
		for i := range v {
			v[i] = redactValue(v[i])
		}
		return v
	case []string:
		for i := range v {
			v[i] = agentkit.SanitizeSecrets(v[i])
		}
		return v
	default:
		return value
	}
}
