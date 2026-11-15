package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/agentkit"
)

// redactingSessionService 在事件落盘前脱敏文本、工具参数与工具结果。
// 它是 ADK session.Service 的装饰器：读取路径保持原样，写入路径统一收口，
// 因此模型后续看到的历史也不会再次携带已识别的密钥。
type redactingSessionService struct {
	session.Service
	artifactDir string
}

const maxInlineToolResponseBytes = 64 * 1024

func newRedactingSessionService(inner session.Service, artifactDir string) session.Service {
	if inner == nil {
		return nil
	}
	return &redactingSessionService{Service: inner, artifactDir: artifactDir}
}

func (r *redactingSessionService) AppendEvent(ctx context.Context, sess session.Session, ev *session.Event) error {
	redactEvent(ev)
	r.spillLargeToolResponses(ev)
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

func (r *redactingSessionService) spillLargeToolResponses(ev *session.Event) {
	if ev == nil || ev.Content == nil {
		return
	}
	for _, part := range ev.Content.Parts {
		if part == nil || part.FunctionResponse == nil {
			continue
		}
		data, err := json.Marshal(part.FunctionResponse.Response)
		if err != nil || len(data) <= maxInlineToolResponseBytes {
			continue
		}
		path := ""
		if r.artifactDir != "" {
			sum := sha256.Sum256(data)
			name := hex.EncodeToString(sum[:12]) + ".json"
			if err := os.MkdirAll(r.artifactDir, 0o755); err == nil {
				full := filepath.Join(r.artifactDir, name)
				if err := os.WriteFile(full, data, 0o600); err == nil {
					path = full
				}
			}
		}
		part.FunctionResponse.Response = map[string]any{
			"ok":        true,
			"truncated": true,
			"bytes":     len(data),
			"artifact":  path,
			"summary":   summarizeToolResponse(data),
			"note":      "完整工具结果已落盘；如需细节请读取 artifact 或缩小查询范围。",
		}
	}
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
