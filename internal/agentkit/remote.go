package agentkit

import (
	"context"
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"google.golang.org/adk/v2/agent"
	remoteagentv2 "google.golang.org/adk/v2/agent/remoteagent/v2"

	"agentmate/internal/config"
)

// BuildRemoteAgent 创建一个 ADK Agent，用于代理远程 A2A Agent（Agent-To-Agent
// 协议，a2aproject/a2a-go）。每次调用时都会从公布的 URL 解析 Agent
// Card，因此无需重启应用即可获取远程侧的更新。
func BuildRemoteAgent(rc config.RemoteAgentConfig) (agent.Agent, error) {
	if rc.CardURL == "" {
		return nil, fmt.Errorf("远程 Agent %q 缺少 Card URL", rc.Name)
	}
	return remoteagentv2.NewA2A(remoteagentv2.A2AConfig{
		Name:              sanitizeAgentName(rc.Name),
		Description:       firstNonEmpty(rc.Description, rc.Name),
		AgentCardProvider: remoteagentv2.NewAgentCardProvider(rc.CardURL),
	})
}

// RemoteAgentInfo 是解析后的 Agent Card 摘要，在检查远程 A2A Agent
// 的连通性时显示在 UI 中。
type RemoteAgentInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	URL         string   `json:"url"`
	Streaming   bool     `json:"streaming"`
	Skills      []string `json:"skills"`
}

// CheckRemoteAgent 解析给定 URL（A2A well-known 位置）上的 Agent Card 并返回
// 摘要；供 UI 的「检查连接」操作使用。
func CheckRemoteAgent(ctx context.Context, cardURL string) (*RemoteAgentInfo, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		return nil, fmt.Errorf("无法获取 Agent Card: %w", err)
	}
	info := &RemoteAgentInfo{
		Name:        card.Name,
		Description: card.Description,
		Version:     card.Version,
		Streaming:   card.Capabilities.Streaming,
	}
	if len(card.SupportedInterfaces) > 0 {
		info.URL = card.SupportedInterfaces[0].URL
	}
	for _, s := range card.Skills {
		info.Skills = append(info.Skills, s.Name)
	}
	return info, nil
}
