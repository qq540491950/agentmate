// Thin re-export layer over the generated Wails bindings.
import { Call } from "@wailsio/runtime";
import * as services from "../../bindings/agentmate/internal/services";
import * as configModels from "../../bindings/agentmate/internal/config/models";
import * as mcpModels from "../../bindings/agentmate/internal/mcpmgr/models";
import * as serviceModels from "../../bindings/agentmate/internal/services/models";
import * as agentkitModels from "../../bindings/agentmate/internal/agentkit/models";

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
export type ChatMessage = serviceModels.ChatMessage;
export type ChatStreamEvent = serviceModels.ChatStreamEvent;
export type ServerStatus = mcpModels.ServerStatus;
export type ToolInfo = mcpModels.ToolInfo;

export type AppConfig = Omit<configModels.Config, "settings"> & {
  settings: Settings;
};
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
export type SandboxStatusInfo = {
  available: boolean;
  backend: string;
  error?: string;
  profileSid?: string;
};
// The split execution-policy fields are newer than the committed bindings
// snapshot. Keep the runtime contract typed until bindings are regenerated.
export type Settings = configModels.Settings & {
  approvalPolicy?: string;
  sandboxProfile?: string;
};
export type ChatSession = configModels.ChatSession & {
  approvalPolicy?: string;
  sandboxProfile?: string;
};
export type Project = configModels.Project;

// Session approval-mode RPC. The backend method is newer than the committed
// bindings snapshot, so call it by fully qualified name through the Wails
// runtime; the method name is the stable contract the generator writes.
const chatServiceName = "agentmate/internal/services.ChatService";
const appServiceName = "agentmate/internal/services.AppService";

/** getSandboxStatus reads the platform backend for Settings warnings. */
export function getSandboxStatus(): Promise<SandboxStatusInfo> {
  return Call.ByName(`${appServiceName}.SandboxStatus`)
    .then((status) => status as SandboxStatusInfo);
}

/** setSessionExecutionPolicy persists the current chat's independent axes. */
export function setSessionExecutionPolicy(
  sessionID: string,
  approvalPolicy: string,
  sandboxProfile: string,
): Promise<void> {
  return Call.ByName(
    `${chatServiceName}.SetSessionExecutionPolicy`,
    sessionID,
    approvalPolicy,
    sandboxProfile,
    "composer",
  ).then(() => undefined);
}

/** @deprecated Use setSessionExecutionPolicy. */
export function setSessionSandboxMode(sessionID: string, mode: string): Promise<void> {
  return Call.ByName(`${chatServiceName}.SetSessionSandboxMode`, sessionID, mode, "composer")
    .then(() => undefined);
}


/** WorkspaceEntry is one "@" picker row: a path relative to the chat workspace. */
export type WorkspaceEntry = serviceModels.WorkspaceEntry;

/** effectiveSkills lists the "/" skills for a session or draft project. */
export function effectiveSkills(sessionID: string, projectID: string): Promise<SkillConfig[]> {
  return Chat.EffectiveSkills(sessionID, projectID).then((list) => list ?? []);
}

/** listContextPaths lists files and folders for the composer "@" picker. */
export function listContextPaths(sessionID: string, projectID: string): Promise<WorkspaceEntry[]> {
  return Artifact.ListContextPaths(sessionID, projectID).then((list) => list ?? []);
}

/** errText extracts a human-readable message from any binding failure. */
export function errText(e: unknown): string {
  if (!e) return "未知错误";
  const anyE = e as { message?: string; cause?: unknown };
  if (anyE.message && anyE.message.trim()) return anyE.message;
  if (anyE.cause != null) return errText(anyE.cause);
  if (typeof e === "string" && e.trim()) return e;
  try { return JSON.stringify(e); } catch { return String(e); }
}

/** uid makes a client-side unique id for streaming message keys. */
let uidCounter = 0;
export function uid(prefix = "u"): string {
  uidCounter += 1;
  return `${prefix}-${Date.now()}-${uidCounter}`;
}
