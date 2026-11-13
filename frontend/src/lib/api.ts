// 基于生成的 Wails 绑定的轻量再导出层。
import * as services from "../../bindings/agentmate/internal/services";
import * as configModels from "../../bindings/agentmate/internal/config/models";
import * as mcpModels from "../../bindings/agentmate/internal/mcpmgr/models";
import * as serviceModels from "../../bindings/agentmate/internal/services/models";
import * as agentkitModels from "../../bindings/agentmate/internal/agentkit/models";
import * as terminalModels from "../../bindings/agentmate/internal/terminal/models";

export const App = services.AppService;
export const A2A = services.A2AService;
export type A2AStatus = serviceModels.A2AStatus;
export const Chat = services.ChatService;
export const Config = services.ConfigService;
export const MCP = services.MCPService;
export const Artifact = services.ArtifactService;
export const Update = services.UpdateService;
export type UpdateStatus = serviceModels.UpdateStatus;

export const Protocol = configModels.Protocol;
export type Protocol = configModels.Protocol;
export const MCPTransport = configModels.MCPTransport;
export type MCPTransport = configModels.MCPTransport;

export type UsageInfo = serviceModels.UsageInfo;
export type Artifact = serviceModels.Artifact;
export type ArtifactInfo = serviceModels.ArtifactInfo;
export type WorkspaceFileInfo = serviceModels.WorkspaceFileInfo;
export type TaskWorkspaceSnapshot = serviceModels.TaskWorkspaceSnapshot;
export const WorkspaceChangeStatus = serviceModels.WorkspaceChangeStatus;
export type WorkspaceChangeStatus = serviceModels.WorkspaceChangeStatus;
export type FileDiffLineType = serviceModels.FileDiffLineType;
export const FileDiffLineType = serviceModels.FileDiffLineType;
export type FileDiffLine = serviceModels.FileDiffLine;
export type FileDiffHunk = serviceModels.FileDiffHunk;
export type FileDiffResult = serviceModels.FileDiffResult;
export const WorkspaceFileKind = serviceModels.WorkspaceFileKind;
export type WorkspaceFileKind = serviceModels.WorkspaceFileKind;
export type AttachmentIn = serviceModels.AttachmentIn;
export type AttachmentOut = serviceModels.AttachmentOut;
export type SessionStats = serviceModels.SessionStats;
export type SkillImportResult = serviceModels.SkillImportResult;
export type ChatMessage = serviceModels.ChatMessage;
export type ChatStreamEvent = serviceModels.ChatStreamEvent;
export type ServerStatus = mcpModels.ServerStatus;
export type ToolInfo = mcpModels.ToolInfo;

export type AppConfig = configModels.Config;
export type ModelProvider = configModels.ModelProvider;
export type ModelInfo = configModels.ModelInfo;
export type ReasoningLevel = configModels.ReasoningLevel;
export type ReasoningConfig = configModels.ReasoningConfig;
export type AgentConfig = configModels.AgentConfig;
export type TeamConfig = configModels.TeamConfig;
export type SkillConfig = configModels.SkillConfig;
export type MCPServerConfig = configModels.MCPServerConfig;
export type RemoteAgentConfig = configModels.RemoteAgentConfig;
export type RemoteAgentInfo = agentkitModels.RemoteAgentInfo;
export type KnowledgeIndexStats = agentkitModels.KnowledgeIndexStats;
export type StorageInfo = serviceModels.StorageInfo;
export type MemoryInfo = serviceModels.MemoryInfo;
export type KnowledgeFile = serviceModels.KnowledgeFile;
export type SandboxStatusInfo = serviceModels.SandboxStatusInfo;
export type Settings = configModels.Settings;
export type TerminalSettings = configModels.TerminalSettings;
export type DetectedTerminal = terminalModels.Detected;
export type TerminalInventory = terminalModels.Inventory;
export type TerminalProbe = terminalModels.ProbeResult;
export type ChatSession = configModels.ChatSession;
export type Project = configModels.Project;

/** getSandboxStatus 读取平台后端以获取设置警告。 */
export function getSandboxStatus(): Promise<SandboxStatusInfo> {
  return App.SandboxStatus();
}

/** setSessionExecutionPolicy 持久化当前聊天的各独立轴。 */
export function setSessionExecutionPolicy(
  sessionID: string,
  approvalPolicy: string,
  sandboxProfile: string,
): Promise<void> {
  return Chat.SetSessionExecutionPolicy(sessionID, approvalPolicy, sandboxProfile, "composer");
}

/** @deprecated 使用 setSessionExecutionPolicy。 */
export function setSessionSandboxMode(sessionID: string, mode: string): Promise<void> {
  return Chat.SetSessionSandboxMode(sessionID, mode, "composer");
}


/** WorkspaceEntry 是 "@" 选择器中的一行：相对于聊天工作区的路径。 */
export type WorkspaceEntry = serviceModels.WorkspaceEntry;

/** effectiveSkills 列出会话或草稿项目的 "/" 技能。 */
export function effectiveSkills(sessionID: string, projectID: string): Promise<SkillConfig[]> {
  return Chat.EffectiveSkills(sessionID, projectID).then((list) => list ?? []);
}

/** listContextPaths 列出供输入区 "@" 选择器使用的文件和文件夹。 */
export function listContextPaths(sessionID: string, projectID: string): Promise<WorkspaceEntry[]> {
  return Artifact.ListContextPaths(sessionID, projectID).then((list) => list ?? []);
}

/** errText 从任何绑定失败中提取人类可读的消息。 */
export function errText(e: unknown): string {
  if (!e) return "未知错误";
  const anyE = e as { message?: string; cause?: unknown };
  if (anyE.message && anyE.message.trim()) return anyE.message;
  if (anyE.cause != null) return errText(anyE.cause);
  if (typeof e === "string" && e.trim()) return e;
  try { return JSON.stringify(e); } catch { return String(e); }
}

/** uid 为流式消息 key 生成客户端唯一 id。 */
let uidCounter = 0;
export function uid(prefix = "u"): string {
  uidCounter += 1;
  return `${prefix}-${Date.now()}-${uidCounter}`;
}
