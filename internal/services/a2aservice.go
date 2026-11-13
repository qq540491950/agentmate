package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/v2/runner"
	adka2av2 "google.golang.org/adk/v2/server/adka2a/v2"
	"google.golang.org/adk/v2/session"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
)

// A2AService 通过 A2A 协议暴露应用的本地 Agent，使其他 A2A
// 客户端（本应用或第三方）可以远程调用它们。它沿用 ADK
// 启动器的服务端方案：Agent 卡片发现 + JSON-RPC 调用。
type A2AService struct {
	S        *Services
	Sessions session.Service

	mu      sync.Mutex
	servers map[string]*a2aServer // agentID -> 运行中的服务器
}

type a2aServer struct {
	srv      *http.Server
	listener net.Listener
	agentID  string
	cardName string
	url      string
}

// NewA2AService 创建该服务。sessions 为远程调用的服务端存储提供后端支持
// （每次远程调用都会获得自己的服务端会话）。
func NewA2AService(s *Services, sessions session.Service) *A2AService {
	return &A2AService{S: s, Sessions: sessions}
}

// A2AStatus 描述一个正在运行的已发布 Agent。
type A2AStatus struct {
	Running   bool   `json:"running"`
	URL       string `json:"url,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
}

// Start 在请求的端口上通过 A2A 发布指定的本地 Agent （0 =
// 选择一个空闲端口）。按 Agent 幂等：重新发布正在运行的 Agent
// 会返回其现有状态。可以同时发布多个 Agent。
func (a *A2AService) Start(agentID string, port int) (A2AStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.servers == nil {
		a.servers = map[string]*a2aServer{}
	}
	if existing, ok := a.servers[agentID]; ok {
		return A2AStatus{Running: true, URL: existing.url, AgentID: agentID, AgentName: existing.cardName}, nil
	}
	cfg := a.S.Store.Get()
	var agentCfg *config.AgentConfig
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == agentID {
			agentCfg = &cfg.Agents[i]
			break
		}
	}
	if agentCfg == nil {
		return A2AStatus{}, fmt.Errorf("agent %q not found", agentID)
	}
	root, err := a.S.Kit.BuildAgent(a2aAgentConfig(*agentCfg))
	if err != nil {
		return A2AStatus{}, err
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return A2AStatus{}, fmt.Errorf("端口监听失败: %w", err)
	}
	name := agentkit.SanitizeAgentName(agentCfg.Name)
	card := &a2acore.AgentCard{
		Name:               name,
		Description:        agentCfg.Description,
		Version:            "1.0.0",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             adka2av2.BuildAgentSkills(root),
		Capabilities:       a2acore.AgentCapabilities{Streaming: true},
		SupportedInterfaces: []*a2acore.AgentInterface{{
			URL:             "http://" + ln.Addr().String() + "/a2a/v1/invoke",
			ProtocolBinding: a2acore.TransportProtocolJSONRPC,
			ProtocolVersion: a2acore.Version,
		}},
	}
	executor := adka2av2.NewExecutor(adka2av2.ExecutorConfig{
		RunnerConfig: runner.Config{
			AppName:        name,
			Agent:          root,
			SessionService: a.Sessions,
		},
	})
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/a2a/v1/invoke", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	entry := &a2aServer{
		srv: srv, listener: ln, agentID: agentID,
		cardName: name, url: "http://" + ln.Addr().String(),
	}
	// 在开始提供服务前注册，以便下方的失败路径能够找到该条目。
	a.servers[agentID] = entry
	go func() {
		// Serve 仅在监听器失败或关闭时返回；无论哪种情况，Agent
		// 都不再可达，因此应将其移除，而不是报告一个仍在运行但拒绝连接的服务器。
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.mu.Lock()
			if cur, ok := a.servers[agentID]; ok && cur.srv == srv {
				delete(a.servers, agentID)
			}
			a.mu.Unlock()
		}
	}()
	return A2AStatus{Running: true, URL: entry.url, AgentID: agentID, AgentName: name}, nil
}

// a2aAgentConfig 使已发布的 Agent 保持非交互状态。A2A 调用路径上没有 UI 可用于
// 回答 HITL 确认，因此继承聊天默认值（request-approval）会使每次文件或 shell
// 调用 永久暂停。保留 workspace-write 的 OS 边界，并清除此运行时副本中各 Agent
// 的提示词； 持久化的 Agent 保持不变。
func a2aAgentConfig(ac config.AgentConfig) config.AgentConfig {
	ac.ApplyExecutionPolicy(config.ApprovalPolicyNever, config.SandboxProfileWorkspaceWrite)
	ac.SandboxNetwork = config.SandboxNetworkOff
	return ac
}

// StopPublishedAgent 是用于配置变更的尽力而为钩子：如果给定的 Agent 当前通过
// A2A 发布，则停止其服务器，使远程客户端无法继续调用已过时（已编辑）或已删除的
// Agent 树。未知 ID 为空操作。
func (s *Services) StopPublishedAgent(agentID string) {
	if s.A2A == nil {
		return
	}
	_ = s.A2A.StopAgent(agentID)
}

// StopAgent 关闭一个已发布 Agent 的服务器。
func (a *A2AService) StopAgent(agentID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.servers[agentID]
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := entry.srv.Shutdown(ctx)
	if err != nil {
		// 流式调用仍在进行时，优雅关闭超时：强制关闭，使状态与实际一致（下方会删除该
		// 条目），并让残留的处理程序停止写入会话存储。
		_ = entry.srv.Close()
	}
	delete(a.servers, agentID)
	return err
}

// StopAll 关闭所有已发布 Agent 的服务器。
func (a *A2AService) StopAll() {
	a.mu.Lock()
	ids := make([]string, 0, len(a.servers))
	for id := range a.servers {
		ids = append(ids, id)
	}
	a.mu.Unlock()
	for _, id := range ids {
		_ = a.StopAgent(id)
	}
}

// Status 列出所有当前已发布的 Agent。
func (a *A2AService) Status() []A2AStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]A2AStatus, 0, len(a.servers))
	for _, e := range a.servers {
		out = append(out, A2AStatus{
			Running: true, URL: e.url, AgentID: e.agentID, AgentName: e.cardName,
		})
	}
	return out
}
