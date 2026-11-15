package services

import (
	"context"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/agentkit"
)

// redactingSessionService 在事件落盘前脱敏文本、工具参数与工具结果。
// 它是 ADK session.Service 的装饰器：读取路径保持原样，写入路径统一收口，
// 因此模型后续看到的历史也不会再次携带已识别的密钥。
type redactingSessionService struct {
	session.Service
}

func newRedactingSessionService(inner session.Service) session.Service {
	if inner == nil {
		return nil
	}
	return &redactingSessionService{Service: inner}
}

func (r *redactingSessionService) AppendEvent(ctx context.Context, sess session.Session, ev *session.Event) error {
	redactEvent(ev)
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
