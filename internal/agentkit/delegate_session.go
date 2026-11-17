package agentkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"google.golang.org/adk/v2/session"
)

const delegateInlineResultBytes = 64 * 1024

// delegateSessionService 是团队成员内存会话的脱敏装饰器：成员的工具参数、
// 结果和文本在写入子会话前清洗，大结果落到 artifact 后再回到协调员。
type delegateSessionService struct {
	session.Service
	artifactDir string
}

func newDelegateSessionService(inner session.Service, artifactDir string) session.Service {
	if inner == nil {
		return nil
	}
	return &delegateSessionService{Service: inner, artifactDir: artifactDir}
}

func (s *delegateSessionService) AppendEvent(ctx context.Context, sess session.Session, ev *session.Event) error {
	if ev != nil {
		if ev.Content != nil {
			for _, part := range ev.Content.Parts {
				if part == nil {
					continue
				}
				part.Text = SanitizeSecrets(part.Text)
				if part.FunctionCall != nil {
					part.FunctionCall.Args = redactSecretsMap(part.FunctionCall.Args)
				}
				if part.FunctionResponse != nil {
					if data, err := json.Marshal(part.FunctionResponse.Response); err == nil && len(data) > delegateInlineResultBytes && s.artifactDir != "" {
						if full := writeDelegateArtifact(s.artifactDir, data); full != "" {
							safeData := []byte(SanitizeSecrets(string(data)))
							part.FunctionResponse.Response = map[string]any{
								"ok": true, "truncated": true, "bytes": len(data),
								"artifact": full, "untrusted_data": true,
								"summary": summarizeGatewayResult(safeData),
							}
						}
					} else {
						part.FunctionResponse.Response = redactSecretsMap(part.FunctionResponse.Response)
					}
				}
			}
		}
		if ev.Actions.StateDelta != nil {
			ev.Actions.StateDelta = redactSecretsMap(ev.Actions.StateDelta)
		}
	}
	return s.Service.AppendEvent(ctx, sess, ev)
}

func writeDelegateArtifact(dir string, data []byte) string {
	safeData := []byte(SanitizeSecrets(string(data)))
	sum := sha256.Sum256(safeData)
	name := hex.EncodeToString(sum[:12]) + ".json"
	full := filepath.Join(dir, "tool-results", name)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(full, safeData, 0o600); err != nil {
		return ""
	}
	return full
}
