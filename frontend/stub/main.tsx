// IPC 桩测试装置：在普通浏览器中通过用预制数据替换 Wails
// 传输层来渲染真实的应用程序 UI。用于新 UI（确认卡片、fork 按钮、远程
// Agent、设置卡片）的视觉回归走查。
import { setTransport } from "@wailsio/runtime";
import React from "react";
import ReactDOM from "react-dom/client";
import App from "../src/App";
import "../src/index.css";

// ---- 方法 ID -----------------------------------------------------------
// Wails 根据 Go 方法全名的 FNV-1a32 哈希来路由 ByID
// 调用。在此处计算它们，而不是硬编码数字：此前重新生成绑定会一次性让每个硬编码
// 的数字键腐坏。
function fnv1a32(text: string): string {
  let hash = 0x811c9dc5;
  for (const byte of new TextEncoder().encode(text)) {
    hash ^= byte;
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return String(hash);
}
const svc = (service: string, method: string) =>
  fnv1a32(`agentmate/internal/services.${service}.${method}`);

// ---- 种子数据 ------------------------------------------------------------

const query = new URLSearchParams(window.location.search);
const queryTheme = ["nord", "graphite", "paper", "contrast"].includes(query.get("theme") ?? "")
  ? query.get("theme")!
  : "nord";
const queryMode = ["light", "dark", "system"].includes(query.get("mode") ?? "")
  ? query.get("mode")!
  : "dark";

const seedConfig = {
  settings: {
    workspaceDir: "/tmp/agentmate-workspace",
    language: "zh-CN",
    themeMode: queryMode,
    themePreset: queryTheme,
    accent: "blue",
    fontScale: "md",
    chatWidth: "medium",
    autoSpeak: false,
    autoTitle: true,
    embeddingProviderId: "prov_1",
    embeddingModel: "text-embedding-3-small",
    executionDisplayMode: "auto",
    sandboxMode: "request-approval",
    sandboxNetwork: "off",
    httpProxy: "",
    autoRemember: false,
    toolsAudit: false,
    compactionInterval: 0,
    compactionContextPercent: 65,
    a2aAutoStart: false,
    a2aAgentId: "",
    a2aPort: 8080,
    terminal: { mode: "auto" },
  },
  providers: [
    {
      id: "prov_1",
      name: "OpenAI",
      protocol: "openai",
      baseUrl: "https://api.openai.com/v1",
      apiKey: "sk-stub",
      models: [
        {
          id: "gpt-5-mini",
          displayName: "GPT-5 Mini",
          multimodal: true,
          contextWindow: 996000,
          maxOutputTokens: 8192,
          priceIn: 2,
          priceOut: 8,
          reasoning: {
            enabled: true,
            defaultLevel: "medium",
            levels: [
              { level: "off", param: "reasoning_effort", value: "none" },
              { level: "low", param: "reasoning_effort", value: "low" },
              { level: "medium", param: "reasoning_effort", value: "medium" },
              { level: "high", param: "reasoning_effort", value: "high" },
              { level: "max", param: "reasoning_effort", value: "max" },
            ],
          },
        },
        {
          id: "gpt-5-max",
          displayName: "GPT-5 Max",
          multimodal: true,
          contextWindow: 400000,
          priceIn: 10,
          priceOut: 40,
        },
      ],
      isDefault: true,
    },
  ],
  agents: [
    {
      id: "agent_1",
      name: "写手助手",
      description: "负责文件写入的演示 Agent",
      providerId: "prov_1",
      model: "gpt-5-mini",
      systemPrompt: "你是写手。",
      builtinTools: ["time", "files", "knowledge"],
      confirmTools: ["files"],
      skillIds: [],
      mcpServerIds: [],
    },
  ],
  remoteAgents: [
    {
      id: "ra_1",
      name: "远程研究助手",
      description: "A2A 远程成员演示",
      cardUrl: "http://127.0.0.1:8080",
      createdAt: "2026-09-18T01:00:00Z",
    },
  ],
  teams: [
    {
      id: "team_1",
      name: "混合团队",
      description: "本地 + 远程成员",
      leadAgentId: "",
      autoCoordinate: true,
      memberAgentIds: ["agent_1"],
      remoteMemberIds: ["ra_1"],
      providerId: "prov_1",
      model: "gpt-5-mini",
      systemPrompt: "",
      createdAt: "2026-09-18T01:00:00Z",
    },
  ],
  // 斜杠菜单测试数据：一个已发现的项目技能和一个应用技能，以便在浏览器桩中看到
  // 来源标签和描述。
  skills: [
    {
      id: "fs:project:weekly-report",
      name: "weekly-report",
      description: "根据本周工作记录撰写结构化周报",
      content: "# 周报\n\n按项目汇总本周进展。",
      createdAt: "",
    },
    {
      id: "skill_review",
      name: "Code Review",
      description: "系统性代码审查清单与输出格式",
      content: "# Code Review\n\n先看正确性，再看可维护性。",
      createdAt: "2026-09-18T01:00:00Z",
    },
  ],
  mcpServers: [
    {
      id: "mcp_context7",
      name: "context7",
      transport: "stdio",
      command: "npx",
      args: ["-y", "@upstash/context7-mcp"],
      env: {},
      url: "",
      headers: {},
      disabled: false,
    },
  ],
  projects: [
    { id: "proj_alpha", name: "cloud", rootDir: "/tmp/proj-alpha", createdAt: "2026-10-01T00:00:00Z" },
    { id: "proj_beta", name: "agentmate", rootDir: "D:/projects/agentmate", createdAt: "2026-10-20T00:00:00Z" },
  ],
  sessions: [
    // 紧凑项目树测试数据：第一个项目的行数少于五行预览，第二个会溢出到「展开显
    // 示」。
    { id: "sess_pm_flash", title: "设计模型等待回复的闪烁状态", targetType: "", targetId: "", createdAt: iso(0, 16), updatedAt: iso(0, 16), projectId: "proj_beta" },
    { id: "sess_cloud_443", title: "我来比对两个配置中的 443 端口节点", targetType: "", targetId: "", createdAt: iso(0, 11), updatedAt: iso(0, 11), projectId: "proj_alpha", pinned: true },
    { id: "sess_cloud_pull1", title: "拉取最新的代码", targetType: "", targetId: "", createdAt: iso(0, 10), updatedAt: iso(0, 10), projectId: "proj_alpha" },
    { id: "sess_cloud_pull2", title: "** 拉取最新代码", targetType: "", targetId: "", createdAt: iso(-1, 18), updatedAt: iso(-1, 18), projectId: "proj_alpha" },
    { id: "sess_cloud_pull3", title: "拉取最新代码", targetType: "", targetId: "", createdAt: iso(-1, 17), updatedAt: iso(-1, 17), projectId: "proj_alpha" },
    { id: "sess_pm_title", title: "优化左侧会话标题提炼与单行显示", targetType: "", targetId: "", createdAt: iso(0, 15), updatedAt: iso(0, 15), projectId: "proj_beta" },
    { id: "sess_pm_scroll", title: "修复滚动到底后箭头仍显示", targetType: "", targetId: "", createdAt: iso(0, 14), updatedAt: iso(0, 14), projectId: "proj_beta" },
    { id: "sess_pm_release", title: "推送代码并触发远端打包", targetType: "", targetId: "", createdAt: iso(0, 13), updatedAt: iso(0, 13), projectId: "proj_beta" },
    { id: "sess_pm_tray", title: "修复托盘恢复窗口未居中", targetType: "", targetId: "", createdAt: iso(0, 12), updatedAt: iso(0, 12), projectId: "proj_beta" },
    { id: "sess_pm_compact", title: "让侧栏项目与对话更紧凑", targetType: "", targetId: "", createdAt: iso(-1, 16), updatedAt: iso(-1, 16), projectId: "proj_beta" },
    { id: "sess_pm_regression", title: "补充项目树视觉回归", targetType: "", targetId: "", createdAt: iso(-1, 15), updatedAt: iso(-1, 15), projectId: "proj_beta" },
    { id: "sess_confirm", title: "需要确认的写入", targetType: "agent", targetId: "agent_1", createdAt: "2026-09-18T01:00:00Z", updatedAt: "2026-09-18T01:05:00Z", modelOverride: "gpt-5-mini", workspaceDir: "/tmp/agentmate-workspace/tasks/sess_confirm" },
    { id: "sess_process", title: "执行过程四档演示", preview: "检查、测试与文件写入均已完成。", targetType: "", targetId: "", createdAt: iso(0, 9), updatedAt: iso(0, 10), sandboxMode: "danger-full-access" },
    { id: "sess_plain", title: "普通对话", targetType: "team", targetId: "team_1", createdAt: "2026-09-17T09:00:00Z", updatedAt: "2026-09-17T09:10:00Z" },
    { id: "sess_direct", title: "我如何选择模型呢？", targetType: "", targetId: "", createdAt: "2026-11-01T00:45:00Z", updatedAt: "2026-11-01T00:48:00Z", sandboxMode: "auto-approve" },
    // 侧边栏分组测试数据：日期相对于加载时间，因此时间分桶（置顶/今天/昨天/7天
    // 内/更早）始终有成员可显示。
    { id: "sess_pinned", title: "置顶：本周迭代计划", targetType: "", targetId: "", createdAt: "2026-10-20T09:00:00Z", updatedAt: "2026-10-20T10:00:00Z", pinned: true },
    { id: "sess_yesterday", title: "整理会议纪要", targetType: "", targetId: "", createdAt: iso(-1, 15), updatedAt: iso(-1, 16) },
    { id: "sess_week", title: "分析导出数据", targetType: "agent", targetId: "agent_1", createdAt: iso(-3, 10), updatedAt: iso(-3, 11) },
    { id: "sess_earlier", title: "重构登录流程", targetType: "", targetId: "", createdAt: iso(-15, 9), updatedAt: iso(-15, 10) },
    { id: "sess_archived", title: "旧版方案讨论", targetType: "", targetId: "", createdAt: iso(-30, 9), updatedAt: iso(-30, 10), archived: true },
    { id: "sess_project", title: "跨目录：独立项目任务", targetType: "agent", targetId: "agent_1", createdAt: iso(0, 8), updatedAt: iso(0, 9), workspaceDir: "/tmp/agentmate-workspace/tasks/sess_project" },
    { id: "sess_projchat", title: "项目内对话：绑定 proj-beta", targetType: "", targetId: "", createdAt: iso(0, 7), updatedAt: iso(0, 7), projectId: "proj_beta" },
    { id: "sess_adhoc", title: "临时目录：未注册工作区", targetType: "", targetId: "", createdAt: iso(0, 6), updatedAt: iso(0, 6), workspaceDir: "/tmp/independent-tools" },
  ],
};

// iso(dayOffset, hour) 构建相对于今天的本地时间 ISO 时间戳。
function iso(dayOffset: number, hour: number): string {
  const d = new Date();
  d.setDate(d.getDate() + dayOffset);
  d.setHours(hour, 0, 0, 0);
  return d.toISOString();
}

const histories: Record<string, any[]> = {
  sess_pm_flash: [
    { id: "flash-u", kind: "user", author: "user", text: "模型首包前显示一个闪动的思考状态。", timestamp: iso(0, 16) },
    { id: "flash-a", kind: "assistant", author: "助手", text: "已接入运行态等待行，并随首包到达自动退出。", timestamp: iso(0, 16) },
  ],
  sess_project: [
    { id: "p1", kind: "user", author: "user", text: "帮我检查这个独立项目的构建产物", timestamp: iso(0, 8) },
    { id: "p2", kind: "assistant", author: "写手助手", text: "这是独立工作区的演示会话（项目分组）。", timestamp: iso(0, 9) },
  ],
  sess_confirm: [
    { id: "m1", kind: "user", author: "user", text: "把周报写到 notes/weekly.md", timestamp: "2026-09-18T01:01:00Z" },
    { id: "m1-plan", kind: "tool_result", author: "写手助手", text: "", toolName: "plan_todos", toolResp: { ok: true, todos: [
      { content: "收集本周素材", status: "completed" },
      { content: "起草周报正文", status: "in_progress" },
      { content: "审阅并写入 weekly.md", status: "pending" },
    ] }, timestamp: "2026-09-18T01:01:05Z" },
    { id: "m2", kind: "tool_call", author: "写手助手", text: "", toolName: "write_workspace_file", toolArgs: { path: "notes/weekly.md", content: "# 周报" }, timestamp: "2026-09-18T01:01:10Z" },
    {
      id: "m3", kind: "confirm_request", author: "写手助手", text: "",
      toolName: "write_workspace_file", toolArgs: { path: "notes/weekly.md", content: "# 周报" },
      callId: "call-confirm-1", hint: "即将写入工作区文件 notes/weekly.md，请确认。",
      timestamp: "2026-09-18T01:01:20Z",
    },
  ],
  sess_plain: [
    { id: "n1", kind: "user", author: "user", text: "介绍下你自己", timestamp: "2026-09-17T09:00:00Z" },
    { id: "n2", kind: "assistant", author: "混合团队", text: "我们是本地 + 远程成员组成的团队，可以协作完成写作与研究任务。", timestamp: "2026-09-17T09:01:00Z" },
  ],
  sess_process: [
    { id: "proc-u", kind: "user", author: "user", text: "检查项目并提交修复", timestamp: iso(0, 9) },
    { id: "proc-t1", kind: "thinking", author: "写手助手", text: "先检查状态，再执行测试，最后提交。", timestamp: iso(0, 9) },
    { id: "proc-c1", kind: "tool_call", author: "写手助手", text: "", toolName: "run_command", toolArgs: { command: "git status --short" }, timestamp: "2026-11-08T09:00:05Z" },
    { id: "proc-r1", kind: "tool_result", author: "写手助手", text: "", toolName: "run_command", toolResp: { stdout: " M internal/config/config.go" }, timestamp: "2026-11-08T09:00:06Z" },
    { id: "proc-n1", kind: "assistant", author: "写手助手", text: "状态检查完成，继续跑测试。", timestamp: "2026-11-08T09:00:07Z" },
    { id: "proc-c2", kind: "tool_call", author: "写手助手", text: "", toolName: "run_command", toolArgs: { command: "go test ./internal/..." }, timestamp: "2026-11-08T09:00:08Z" },
    { id: "proc-r2", kind: "tool_result", author: "写手助手", text: "", toolName: "run_command", toolResp: { ok: true }, timestamp: "2026-11-08T09:00:14Z" },
    { id: "proc-c4", kind: "tool_call", author: "写手助手", text: "", toolName: "read_workspace_file", toolArgs: { path: "internal/agentkit/shell.go" }, timestamp: "2026-11-08T09:00:14.2Z" },
    { id: "proc-r4", kind: "tool_result", author: "写手助手", text: "", toolName: "read_workspace_file", toolResp: { ok: true, bytes: 2048 }, timestamp: "2026-11-08T09:00:14.4Z" },
    { id: "proc-c5", kind: "tool_call", author: "写手助手", text: "", toolName: "run_command", toolArgs: { command: "rg -n \"SysProcAttr\" internal/agentkit" }, timestamp: "2026-11-08T09:00:14.6Z" },
    { id: "proc-r5", kind: "tool_result", author: "写手助手", text: "", toolName: "run_command", toolResp: { stdout: "shell_windows.go:24" }, timestamp: "2026-11-08T09:00:14.8Z" },
    { id: "proc-c6", kind: "tool_call", author: "写手助手", text: "", toolName: "read_workspace_file", toolArgs: { path: "internal/services/chatservice_turn.go" }, timestamp: "2026-11-08T09:00:15Z" },
    { id: "proc-r6", kind: "tool_result", author: "写手助手", text: "", toolName: "read_workspace_file", toolResp: { ok: true, bytes: 4096 }, timestamp: "2026-11-08T09:00:15.2Z" },
    { id: "proc-c7", kind: "tool_call", author: "写手助手", text: "", toolName: "run_command", toolArgs: { command: "go doc syscall.SysProcAttr 2>&1" }, timestamp: "2026-11-08T09:00:15.4Z" },
    { id: "proc-r7", kind: "tool_result", author: "写手助手", text: "", toolName: "run_command", toolResp: { error: "文档未找到" }, timestamp: "2026-11-08T09:00:15.6Z" },
    { id: "proc-c8", kind: "tool_call", author: "写手助手", text: "", toolName: "search_knowledge", toolArgs: { query: "执行过程显示" }, timestamp: "2026-11-08T09:00:15.8Z" },
    { id: "proc-r8", kind: "tool_result", author: "写手助手", text: "", toolName: "search_knowledge", toolResp: { ok: true, refs: [] }, timestamp: "2026-11-08T09:00:16Z" },
    { id: "proc-c9", kind: "tool_call", author: "写手助手", text: "", toolName: "get_current_time", toolArgs: {}, timestamp: "2026-11-08T09:00:16.2Z" },
    { id: "proc-r9", kind: "tool_result", author: "写手助手", text: "", toolName: "get_current_time", toolResp: { time: "2026-11-08T17:00:16Z" }, timestamp: "2026-11-08T09:00:16.4Z" },
    { id: "proc-c3", kind: "tool_call", author: "写手助手", text: "", toolName: "write_workspace_file", toolArgs: { path: "notes/weekly.md", content: "# 周报" }, timestamp: "2026-11-08T09:00:16.6Z" },
    { id: "proc-r3", kind: "tool_result", author: "写手助手", text: "", toolName: "write_workspace_file", toolResp: { ok: true, bytes: 8 }, timestamp: "2026-11-08T09:00:16.8Z" },
    { id: "proc-a", kind: "assistant", author: "写手助手", text: "检查、测试与文件写入均已完成。", timestamp: "2026-11-08T09:00:18Z" },
  ],
  sess_direct: [
    { id: "d1", kind: "user", author: "user", text: "Q9 可以参考其他的 agent 的工具是如何处理的？比如单文件 48KB 上限、单轮 96KB、二进制检测这些阈值，各家 CLI 是静默截断还是显式报错，社区有没有已经形成的共识做法？", timestamp: "2026-11-01T00:46:00Z" },
    {
      id: "d2", kind: "assistant", author: "助手", timestamp: "2026-11-01T00:46:20Z",
      text: [
        "选择 AI 模型时，不要只看参数规模，建议按下面几步：",
        "## 1. 先明确任务类型",
        "- **复杂推理、研究、复杂写作**：选能力最强的通用模型。",
        "- **日常问答、分类、摘要、批量处理**：选速度快、价格低的小模型。",
        "- **编程、代码审查**：选代码能力强的模型。",
        "- **图片、语音、视频理解**：选对应的多模态模型。",
        "- **敏感数据或离线使用**：优先考虑可私有部署的本地模型。",
        "## 2. 比较关键指标",
        "1. **效果**：准确性、指令遵循、是否容易出错",
        "2. **速度**：首字响应和完整响应时间",
        "3. **成本**：按 Token 计费时尤其重要",
        "4. **上下文长度**：长文档处理需要更大窗口",
        "5. **工具调用**：Agent 工作流需要稳定的函数/工具调用能力",
        "6. **隐私与合规**：数据是否会被保存或用于训练",
        "7. **稳定性与可用性**：服务限制、速率、版本兼容",
        "## 3. 用自己的测试题验证",
        "准备 20～50 个最接近真实工作的例子，同时比较：",
        "- 答案正确率",
        "- 完成率",
        "- 响应时间",
        "- 单次成本",
        "- 格式和风格是否符合要求",
        "## 4. 实用的选择原则",
        "> 简单任务用便宜快速模型，困难任务用强模型。",
        "例如采用“模型路由”：",
        "- 分类、改写、提取信息 → 小模型",
        "- 复杂分析、方案设计、疑难排错 → 大模型",
        "- 先由小模型处理，检测到困难后再升级",
        "如果你告诉我具体用途，例如写作、编程、客服、图片识别或批量数据处理，我可以直接给你一个模型选择方案。",
      ].join("\n"),
    },
    { id: "d3", kind: "user", author: "user", text: "按这个调研结论，给出我们 chat_context.go 的调整清单与回归测试点", timestamp: "2026-11-01T00:50:00Z" },
    {
      id: "d4", kind: "assistant", author: "助手", timestamp: "2026-11-01T00:50:30Z",
      text: [
        "结合调研结论，建议分三步调整：",
        "- **阈值对齐**：单文件上限对齐主流的 32KB，超出时显式提示而不是静默截断。",
        "- **预算提示**：单轮接近 96KB 时在输入区给出剩余额度。",
        "- **回归测试**：覆盖越界文件、二进制文件、引号路径三类用例。",
      ].join("\n"),
    },
  ],
};

let pendingAnswer = 0;
// 会话级审批模式，合并到 ListSessions
// 中，以便在桩中切换聊天时恢复每个聊天自身的模式。
const sessionSandboxModes = new Map<string, string>();
type TaskSnapshotMode = "success" | "empty" | "error" | "truncated" | "slow";
// 浏览器走查可以通过 ?taskMode= 请求 empty/error/truncated/slow 状态。
const stubParams = new URLSearchParams(window.location.search);
const requestedTaskSnapshotMode = stubParams.get("taskMode");
let taskSnapshotMode: TaskSnapshotMode = (["success", "empty", "error", "truncated", "slow"] as const).includes(
  requestedTaskSnapshotMode as TaskSnapshotMode,
) ? requestedTaskSnapshotMode as TaskSnapshotMode : "success";
const workspaceMockMode = stubParams.get("workspaceMode") === "conflict"
  ? "conflict"
  : stubParams.get("workspaceMode") === "error" ? "error" : "success";
const dialogMode = stubParams.get("dialogMode") === "cancel" ? "cancel" : "select";
// 更新流程夹具：?update=hidden|available|downloading|ready|error|notify
const requestedUpdateMode = stubParams.get("update") ?? "available";
const updateModes = ["hidden", "available", "downloading", "ready", "error", "notify"] as const;
type UpdateMode = (typeof updateModes)[number];
let updateMode: UpdateMode = (updateModes as readonly string[]).includes(requestedUpdateMode)
  ? (requestedUpdateMode as UpdateMode)
  : "available";
let updateState = updateMode === "hidden" ? "up-to-date" : updateMode === "notify" ? "available" : updateMode;
let updateProgress = { written: 12_400_000, total: 29_800_000, rate: 3_100_000 };

function stubUpdateStatus() {
  return {
    currentVersion: "0.6.3",
    state: updateState,
    mode: updateMode === "notify" ? "notify-only" : "auto-install",
    reason: updateMode === "notify" ? "当前安装方式无法自动替换应用，请打开发布页下载。" : "",
    availableVersion: updateState === "up-to-date" ? "" : "0.6.4",
    notes: "• 侧栏直接显示更新进度，完成后可一键重启\n• 改进长会话滚动定位与工具调用展示\n• 修复托盘恢复、代理连接等 12 项问题",
    autoCheck: true,
    skippedVersion: "",
    error: updateState === "error" ? "连接更新服务器失败，请检查网络后重试。" : "",
    downloadUrl: "https://github.com/qq540491950/agentmate/releases",
    canAutoInstall: updateMode !== "notify",
  };
}

const handlers: Record<string, (args: any[]) => any> = {
  // GetConfig
  [svc("ConfigService", "GetConfig")]: () => seedConfig,
  // SaveSettings：设置页面共享一条显式保存路径，因此 stub
  // 必须持久化草稿，以便保存后清除脏状态。
  [svc("ConfigService", "SaveSettings")]: (args) => {
    seedConfig.settings = args[0];
    return null;
  },
  [svc("ConfigService", "DetectTerminals")]: (args) => {
    const danger = args[0] === "danger-full-access";
    return {
      os: "windows",
      auto: {
        mode: "auto",
        displayName: danger ? "自动（Git Bash）" : "自动（Windows PowerShell 5.1）",
        path: danger ? "C:\\Program Files\\Git\\usr\\bin\\bash.exe" : "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
        source: danger ? "git" : "system",
        sandbox: danger ? "requires-full-access" : "ok",
        autoEligible: true,
      },
      shells: [
        { mode: "pwsh", displayName: "PowerShell 7+", path: "C:\\Users\\me\\AppData\\Local\\Microsoft\\WindowsApps\\pwsh.exe", version: "7.7.0", source: "store-alias", sandbox: "unknown", autoEligible: false },
        { mode: "git-bash", displayName: "Git Bash", path: "C:\\Program Files\\Git\\usr\\bin\\bash.exe", version: "GNU bash, version 5.2", source: "git", sandbox: "requires-full-access", autoEligible: true },
        { mode: "powershell", displayName: "Windows PowerShell 5.1", path: "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe", version: "5.1", source: "system", sandbox: "ok", autoEligible: true },
        { mode: "cmd", displayName: "命令提示符（cmd.exe）", path: "C:\\Windows\\System32\\cmd.exe", version: "10.0", source: "system", sandbox: "ok", autoEligible: true },
      ],
      wsl: [
        { mode: "wsl", displayName: "WSL：Ubuntu-24.04", path: "C:\\Windows\\System32\\wsl.exe", source: "wsl", sandbox: "requires-full-access", autoEligible: false, wslDistro: "Ubuntu-24.04" },
      ],
      wslDistros: ["Ubuntu-24.04"],
      errors: [],
    };
  },
  "agentmate/internal/services.ConfigService.DetectTerminals": (args) => {
    const danger = args[0] === "danger-full-access";
    return {
      os: "windows",
      auto: {
        mode: "auto",
        displayName: danger ? "自动（Git Bash）" : "自动（Windows PowerShell 5.1）",
        path: danger ? "C:\\Program Files\\Git\\usr\\bin\\bash.exe" : "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
        source: danger ? "git" : "system",
        sandbox: danger ? "requires-full-access" : "ok",
        autoEligible: true,
      },
      shells: [
        { mode: "pwsh", displayName: "PowerShell 7+", path: "C:\\Users\\me\\AppData\\Local\\Microsoft\\WindowsApps\\pwsh.exe", version: "7.7.0", source: "store-alias", sandbox: "unknown", autoEligible: false },
        { mode: "git-bash", displayName: "Git Bash", path: "C:\\Program Files\\Git\\usr\\bin\\bash.exe", version: "GNU bash, version 5.2", source: "git", sandbox: "requires-full-access", autoEligible: true },
        { mode: "powershell", displayName: "Windows PowerShell 5.1", path: "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe", version: "5.1", source: "system", sandbox: "ok", autoEligible: true },
        { mode: "cmd", displayName: "命令提示符（cmd.exe）", path: "C:\\Windows\\System32\\cmd.exe", version: "10.0", source: "system", sandbox: "ok", autoEligible: true },
      ],
      wsl: [
        { mode: "wsl", displayName: "WSL：Ubuntu-24.04", path: "C:\\Windows\\System32\\wsl.exe", source: "wsl", sandbox: "requires-full-access", autoEligible: false, wslDistro: "Ubuntu-24.04" },
      ],
      wslDistros: ["Ubuntu-24.04"],
      errors: [],
    };
  },
  [svc("ConfigService", "TestTerminal")]: () => ({
    mode: "powershell",
    displayName: "Windows PowerShell 5.1",
    path: "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
    output: "agentmate-shell-ok",
    exitCode: 0,
    sandbox: "ok",
  }),
  "agentmate/internal/services.ConfigService.TestTerminal": () => ({
    mode: "powershell",
    displayName: "Windows PowerShell 5.1",
    path: "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
    output: "agentmate-shell-ok",
    exitCode: 0,
    sandbox: "ok",
  }),
  // ListSessions
  [svc("ChatService", "ListSessions")]: () => {
    const sessions = seedConfig.sessions.map((session) => ({
      ...session,
      sandboxMode: sessionSandboxModes.get(session.id) ?? (session as any).sandboxMode ?? "request-approval",
    }));
    (window as any).__lastList = JSON.parse(JSON.stringify(sessions));
    return sessions;
  },
  // GetSessionMessages(sessionID)
  [svc("ChatService", "GetSessionMessages")]: (args) => histories[args[0]] ?? [],
  // IsRunning(sessionID)
  [svc("ChatService", "IsRunning")]: () => false,
  // SessionStats(sessionID)：这些数字与 composer
  // 的「背景信息窗口」悬停卡片一致（996k 窗口中的 109k ≈ 11%，外加 96k
  // 缓存命中），因此无需实际模型调用即可走查该卡片。
  [svc("ChatService", "SessionStats")]: () => ({
    messages: 1, toolCalls: 1, toolFailures: 0, assistantTurns: 0,
    tokensIn: 109000, tokensOut: 0, tokensTotal: 109000,
    contextTokens: 109000, cachedTokens: 96000,
  }),
  // ArchiveSession(sessionID, archived)
  [svc("ChatService", "ArchiveSession")]: (args) => {
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (!sess) throw new Error("会话不存在");
    (sess as any).archived = !!args[1];
    return null;
  },
  // PinSession(sessionID, pinned)
  [svc("ChatService", "PinSession")]: (args) => {
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (!sess) throw new Error("会话不存在");
    (sess as any).pinned = !!args[1];
    return null;
  },
  // RefineSessionTitle(sessionID)
  [svc("ChatService", "RefineSessionTitle")]: (args) => {
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (!sess) throw new Error("会话不存在");
    const title = "提炼后的会话标题";
    (sess as any).title = title;
    return title;
  },
  // DeleteSession(sessionID)
  [svc("ChatService", "DeleteSession")]: (args) => {
    seedConfig.sessions = seedConfig.sessions.filter((s) => s.id !== args[0]);
    delete histories[args[0]];
    return null;
  },
  // SetSessionModel(sessionID, modelID)
  [svc("ChatService", "SetSessionModel")]: (args) => {
    for (const sess of seedConfig.sessions) {
      if (sess.id === args[0]) {
        sess.modelOverride = args[1] || "";
        (sess as any).reasoningLevel = "";
      }
    }
    return null;
  },
  // SetSessionReasoningLevel(sessionID, level)
  [svc("ChatService", "SetSessionReasoningLevel")]: (args) => {
    for (const sess of seedConfig.sessions) {
      if (sess.id === args[0]) (sess as any).reasoningLevel = args[1] || "";
    }
    return null;
  },
  // SendWithReasoning(sessionID, text, attachments, reasoningLevel)
  [svc("ChatService", "SendWithReasoning")]: (args) => {
    const [sid, text, , reasoning] = args;
    const stamp = new Date().toISOString();
    pendingAnswer += 1;
    const answer = `收到：${text}`;
    const userMsg = { id: `m-user-${pendingAnswer}`, kind: "user", author: "user", text, timestamp: stamp, ...(reasoning ? { reasoning } : {}) };
    histories[sid] = [
      ...(histories[sid] ?? []),
      userMsg,
      { id: `m-ans-${pendingAnswer}`, kind: "assistant", author: "助手", text: answer, timestamp: stamp },
    ];
    // 镜像后端流式：用户气泡（带该回合的级别）、回复、完成。
    queueMicrotask(() => {
      window.__emit("chat:stream", { sessionId: sid, kind: "user", text, ...(reasoning ? { reasoning } : {}) });
      window.__emit("chat:stream", { sessionId: sid, kind: "message", author: "助手", text: answer });
      window.__emit("chat:stream", { sessionId: sid, kind: "done" });
    });
    return null;
  },
  // NewSessionWithWorkspace(targetType, targetID, title, workspaceDir)
  [svc("ChatService", "NewSessionWithWorkspace")]: (args) => {
    if (workspaceMockMode === "conflict") throw new Error("工作区已被任务「需要确认的写入」使用：归档或删除该任务即可释放目录，也可以选择其他目录");
    if (workspaceMockMode === "error") throw new Error("工作区不可写");
    const id = `sess_stub_${seedConfig.sessions.length + 1}`;
    const sess = {
      id,
      title: "新对话",
      targetType: args[0],
      targetId: args[1],
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      workspaceDir: args[3] || `/tmp/agentmate-workspace/tasks/${id}`,
    };
    seedConfig.sessions.unshift(sess);
    histories[id] = [];
    return sess;
  },
  // SetSessionWorkspace(sessionID, workspaceDir)
  [svc("ChatService", "SetSessionWorkspace")]: (args) => {
    if (workspaceMockMode === "conflict") throw new Error("工作区已被任务「需要确认的写入」使用：归档或删除该任务即可释放目录，也可以选择其他目录");
    if (workspaceMockMode === "error") throw new Error("工作区不可写");
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (sess) sess.workspaceDir = args[1];
    return null;
  },
  // SessionWorkspace(sessionID)
  [svc("ChatService", "SessionWorkspace")]: (args) => {
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (!sess) throw new Error("会话不存在");
    const project = seedConfig.projects.find((p: any) => p.id === (sess as any).projectId);
    return sess.workspaceDir || project?.rootDir || seedConfig.settings.workspaceDir;
  },
  // ListProjects
  [svc("ChatService", "ListProjects")]: () => seedConfig.projects,
  // SaveProject(id, name, rootDir)
  [svc("ChatService", "SaveProject")]: (args) => {
    const [id, name, rootDir] = args;
    if (!id) {
      const proj = {
        id: `proj_stub_${seedConfig.projects.length + 1}`,
        name: name || rootDir.split("/").pop(),
        rootDir,
        createdAt: new Date().toISOString(),
      };
      seedConfig.projects.push(proj);
      return proj;
    }
    const proj = seedConfig.projects.find((p: any) => p.id === id);
    if (!proj) throw new Error("项目不存在");
    if (name) proj.name = name;
    if (rootDir) proj.rootDir = rootDir;
    return proj;
  },
  // DeleteProject(id)
  [svc("ChatService", "DeleteProject")]: (args) => {
    seedConfig.projects = seedConfig.projects.filter((p: any) => p.id !== args[0]);
    for (const sess of seedConfig.sessions) {
      if ((sess as any).projectId === args[0]) delete (sess as any).projectId;
    }
    return null;
  },
  // NewSessionWithProject(targetType, targetID, title, projectID)
  [svc("ChatService", "NewSessionWithProject")]: (args) => {
    const id = `sess_stub_${seedConfig.sessions.length + 1}`;
    const sess = {
      id,
      title: "新对话",
      targetType: args[0],
      targetId: args[1],
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      projectId: args[3],
    };
    seedConfig.sessions.unshift(sess);
    histories[id] = [];
    return sess;
  },
  // ArtifactService.TaskWorkspaceSnapshot(sessionID)
  [svc("ArtifactService", "TaskWorkspaceSnapshot")]: async (args) => {
    if (taskSnapshotMode === "slow") {
      await new Promise((resolve) => setTimeout(resolve, 700));
    }
    if (taskSnapshotMode === "error") throw new Error("任务工作区不可读");
    const sessions = seedConfig.sessions as Array<{ id: string; workspaceDir?: string }>;
    const workspace = sessions.find((s) => s.id === args[0])?.workspaceDir || seedConfig.settings.workspaceDir;
    const base = {
      sessionId: args[0],
      workspace,
      discovered: 0,
      added: 0,
      modified: 0,
      deleted: 0,
      truncated: false,
      baselineAt: "2026-09-18T01:00:00Z",
      refreshedAt: new Date().toISOString(),
    };
    if (taskSnapshotMode === "empty") {
      return { ...base, files: [], artifacts: [], changes: [] };
    }
    if (taskSnapshotMode === "truncated") {
      return {
        ...base,
        files: [
          { name: "huge.log", path: "logs/huge.log", kind: "text", size: 532480, modTime: "2026-09-18T01:08:00Z", previewable: true },
        ],
        artifacts: [],
        changes: [],
        discovered: 5001,
        truncated: true,
      };
    }
    return {
      ...base,
      files: [
      { name: "report.md", path: "output/report.md", kind: "markdown", size: 7168, modTime: "2026-09-18T01:07:00Z", previewable: true },
      { name: "weekly.md", path: "notes/weekly.md", kind: "markdown", size: 2048, modTime: "2026-09-18T01:06:00Z", previewable: true },
      { name: "chart.png", path: "assets/chart.png", kind: "image", size: 40960, modTime: "2026-09-18T01:05:00Z", previewable: true },
      { name: "archive.zip", path: "archive.zip", kind: "binary", size: 524288, modTime: "2026-09-17T09:00:00Z", previewable: false },
      ],
      artifacts: [
      { name: "report.md", path: "output/report.md", kind: "markdown", size: 7168, modTime: "2026-09-18T01:07:00Z", previewable: true, status: "added" },
      { name: "weekly.md", path: "notes/weekly.md", kind: "markdown", size: 2048, modTime: "2026-09-18T01:06:00Z", previewable: true, status: "modified" },
      ],
      changes: [
      { name: "report.md", path: "output/report.md", kind: "markdown", size: 7168, modTime: "2026-09-18T01:07:00Z", previewable: true, status: "added" },
      { name: "weekly.md", path: "notes/weekly.md", kind: "markdown", size: 2048, modTime: "2026-09-18T01:06:00Z", previewable: true, status: "modified" },
      { name: "old.txt", path: "notes/old.txt", kind: "text", size: 512, modTime: "2026-09-17T09:00:00Z", previewable: true, status: "deleted" },
      ],
      discovered: 4,
      added: 1,
      modified: 1,
      deleted: 1,
    };
  },
  // ArtifactService.OpenForSession(sessionID, path)
  [svc("ArtifactService", "OpenForSession")]: (args) => ({
    name: String(args[1]).split("/").pop(),
    path: args[1],
    kind: args[1].endsWith(".png") ? "image" : "markdown",
    mime: args[1].endsWith(".png") ? "image/png" : "text/markdown",
    size: 2048,
    text: args[1].endsWith(".png") ? undefined : "# 周报\n\n任务文件预览。",
  }),
  // ArtifactService.FileDiff(sessionID, relPath) — 与种子变更列表对应的夹具。
  [svc("ArtifactService", "FileDiff")]: (args) => {
    const path = String(args[1]);
    if (path === "archive.zip") {
      return { sessionId: args[0], path, status: "modified", unsupported: "二进制文件不支持 diff", hunks: [] };
    }
    if (path === "notes/old.txt") {
      return {
        sessionId: args[0], path, status: "deleted", oldSize: 512,
        addedLines: 0, removedLines: 6, hunks: [{
          oldStart: 1, oldLines: 6, newStart: 0, newLines: 0,
          lines: [
            { type: "del", oldNo: 1, text: "周报（旧版，已被 weekly.md 取代）" },
            { type: "del", oldNo: 2, text: "" },
            { type: "del", oldNo: 3, text: "## 本周进展" },
            { type: "del", oldNo: 4, text: "- 完成初稿" },
            { type: "del", oldNo: 5, text: "- 收集反馈" },
            { type: "del", oldNo: 6, text: "- 待评审" },
          ],
        }],
      };
    }
    if (path === "notes/weekly.md") {
      return {
        sessionId: args[0], path, status: "modified", oldSize: 2048, newSize: 2184,
        addedLines: 3, removedLines: 1, hunks: [{
          oldStart: 3, oldLines: 7, newStart: 3, newLines: 9,
          lines: [
            { type: "context", oldNo: 3, newNo: 3, text: "## 本周进展" },
            { type: "del", oldNo: 4, text: "- 完成知识库检索联调" },
            { type: "add", newNo: 4, text: "- 完成知识库检索联调（含混合排序回归）" },
            { type: "add", newNo: 5, text: "- 任务结果区新增行级 diff 视图" },
            { type: "add", newNo: 6, text: "- 基线内容快照与清理链路打通" },
            { type: "context", oldNo: 5, newNo: 7, text: "- 修复切会话闪烁" },
            { type: "context", oldNo: 6, newNo: 8, text: "" },
            { type: "context", oldNo: 7, newNo: 9, text: "## 下周计划" },
          ],
        }],
      };
    }
    return {
      sessionId: args[0], path, status: "added", newSize: 7168,
      addedLines: 12, removedLines: 0, hunks: [{
        oldStart: 0, oldLines: 0, newStart: 1, newLines: 12,
        lines: [
          { type: "add", newNo: 1, text: "# 项目周报" },
          { type: "add", newNo: 2, text: "" },
          { type: "add", newNo: 3, text: "## 概要" },
          { type: "add", newNo: 4, text: "本周完成侧栏重排与项目分类，结果区支持行级 diff。" },
          { type: "add", newNo: 5, text: "" },
          { type: "add", newNo: 6, text: "## 明细" },
          { type: "add", newNo: 7, text: "- 新聊天按钮与项目手风琴" },
          { type: "add", newNo: 8, text: "- 最近列表时间分组" },
          { type: "add", newNo: 9, text: "- 输入区上下文标签" },
          { type: "add", newNo: 10, text: "- 变更视图 diff（本文件即为新增示例）" },
          { type: "add", newNo: 11, text: "" },
          { type: "add", newNo: 12, text: "## 风险" },
        ],
      }],
    };
  },
  // AppInfo
  [svc("AppService", "AppInfo")]: () => ({ name: "AgentMate", version: "0.6.3", adk: "google adk-go v2" }),
  // UpdateService — 浏览器走查可通过
  // ?update=hidden|available|downloading|ready|error|notify
  // 从任意更新器状态开始。
  [svc("UpdateService", "Status")]: () => stubUpdateStatus(),
  [svc("UpdateService", "CheckNow")]: () => stubUpdateStatus(),
  [svc("UpdateService", "InstallUpdate")]: async () => {
    updateMode = "downloading";
    updateState = "downloading";
    (window as any).__emit?.("wails:updater:download-started", null);
    for (const ratio of [0.08, 0.21, 0.42, 0.67, 0.88, 1]) {
      await new Promise((resolve) => setTimeout(resolve, 180));
      updateProgress = {
        written: Math.round(updateProgress.total * ratio),
        total: updateProgress.total,
        rate: 3_100_000,
      };
      (window as any).__emit?.("wails:updater:download-progress", updateProgress);
    }
    updateState = "verifying";
    (window as any).__emit?.("wails:updater:verifying", null);
    await new Promise((resolve) => setTimeout(resolve, 220));
    updateState = "installing";
    (window as any).__emit?.("wails:updater:installing", null);
    await new Promise((resolve) => setTimeout(resolve, 220));
    updateState = "ready";
    (window as any).__emit?.("wails:updater:update-ready", null);
    return stubUpdateStatus();
  },
  [svc("UpdateService", "ApplyUpdate")]: () => {
    (window as any).__updateApplied = true;
    return null;
  },
  [svc("UpdateService", "OpenDownloadPage")]: () => {
    (window as any).__openedReleasePage = true;
    return null;
  },
  [svc("UpdateService", "Skip")]: () => {
    updateMode = "hidden";
    updateState = "up-to-date";
    (window as any).__emit?.("wails:updater:no-update", null);
    return null;
  },
  [svc("UpdateService", "SetAutoCheck")]: () => null,
  // AppService.RevealWorkspaceFile(sessionID, relPath) — 桩仅记录调用
  [svc("AppService", "RevealWorkspaceFile")]: (args) => {
    (window).__revealed = ((window).__revealed ?? []).concat(args[1]);
    return null;
  },
  // ChatService.DeleteArchivedSessions() — 清空种子里的归档会话
  [svc("ChatService", "DeleteArchivedSessions")]: () => {
    const before = seedConfig.sessions.length;
    seedConfig.sessions = seedConfig.sessions.filter((x: any) => !x.archived);
    return before - seedConfig.sessions.length;
  },
  // ConfirmToolCall(sessionID, callID, approved)
  [svc("ChatService", "ConfirmToolCall")]: (args) => {
    pendingAnswer++;
    const approved = args[2] === true;
    histories[args[0]] = [
      ...(histories[args[0]] ?? []),
      { id: `m-answer-${pendingAnswer}`, kind: "confirm_result", author: "user", text: "", callId: args[1], toolResp: { confirmed: approved }, timestamp: "2026-09-18T01:06:00Z" },
      ...(approved
        ? [{ id: `m-result-${pendingAnswer}`, kind: "tool_result", author: "写手助手", text: "", toolName: "write_workspace_file", toolResp: { ok: true, bytes: 6 }, timestamp: "2026-09-18T01:06:01Z" },
           { id: `m-final-${pendingAnswer}`, kind: "assistant", author: "写手助手", text: "已确认并写入 notes/weekly.md。", timestamp: "2026-09-18T01:06:02Z" }]
        : [{ id: `m-reject-${pendingAnswer}`, kind: "assistant", author: "写手助手", text: "好的，已取消写入。", timestamp: "2026-09-18T01:06:02Z" }]),
    ];
    // 将确认恢复流程流式回传到 UI。
    queueMicrotask(() => {
      window.__emit("chat:stream", { sessionId: args[0], kind: "tool_result", author: "写手助手", toolName: "write_workspace_file", toolResp: { ok: true, bytes: 6 } });
      window.__emit("chat:stream", { sessionId: args[0], kind: "message", author: "写手助手", text: approved ? "已确认并写入 notes/weekly.md。" : "好的，已取消写入。" });
      window.__emit("chat:stream", { sessionId: args[0], kind: "done" });
    });
    return null;
  },
  // ChatService.SessionSandboxMode(sessionID) / SetSessionSandboxMode。这些 RPC
  // 按名称调用，因此 stub 也会根据 methodName 进行路由。
  "agentmate/internal/services.ChatService.SessionSandboxMode": (args) => {
    const id = String(args[0] ?? "");
    return sessionSandboxModes.get(id)
      ?? seedConfig.sessions.find((session) => session.id === id)?.sandboxMode
      ?? "request-approval";
  },
  "agentmate/internal/services.ChatService.SetSessionSandboxMode": (args) => {
    const id = String(args[0] ?? "");
    const mode = String(args[1] ?? "request-approval");
    sessionSandboxModes.set(id, mode);
    const session = seedConfig.sessions.find((item) => item.id === id);
    if (session) session.sandboxMode = mode;
    return null;
  },
  // ChatService.EffectiveSkills(sessionID, projectID) — composer
  // 的“/”菜单。数字键 = 生成绑定的 ByID 路由；ByName 键让较旧的快照继续工作。
  [svc("ChatService", "EffectiveSkills")]: () => seedConfig.skills,
  "agentmate/internal/services.ChatService.EffectiveSkills": () => seedConfig.skills,
  // ArtifactService.ListContextPaths(sessionID, projectID) — composer
  // 的“@”菜单。
  [svc("ArtifactService", "ListContextPaths")]: () => [
    { path: "notes", dir: true },
    { path: "notes/weekly.md", dir: false },
    { path: "src", dir: true },
    { path: "src/main.go", dir: false },
    { path: "README.md", dir: false },
  ],
  "agentmate/internal/services.ArtifactService.ListContextPaths": () => [
    { path: "notes", dir: true },
    { path: "notes/weekly.md", dir: false },
    { path: "src", dir: true },
    { path: "src/main.go", dir: false },
    { path: "README.md", dir: false },
  ],
  // ListProviderModels
  [svc("ConfigService", "ListProviderModels")]: () => ["gpt-5-mini", "gpt-5"],
  // SaveMCPServer 会镜像规范化后的已保存服务器契约，使 MCP
  // 页面可以立即应用启用/禁用的期望状态。
  [svc("ConfigService", "SaveMCPServer")]: (args) => {
    const saved = args[0];
    const index = seedConfig.mcpServers.findIndex((s: any) => s.id === saved.id);
    if (index >= 0) seedConfig.mcpServers[index] = saved;
    else seedConfig.mcpServers.push(saved);
    return saved;
  },
  [svc("MCPService", "Status")]: () => [{
    id: "mcp_context7",
    name: "context7",
    state: "connected",
    tools: [{ name: "resolve-library-id", description: "Resolve a library ID" }],
  }],
  [svc("MCPService", "Connect")]: (args) => ({
    id: args[0],
    name: "context7",
    state: "connected",
    tools: [{ name: "resolve-library-id", description: "Resolve a library ID" }],
  }),
  // SuggestModels(ids)
  [svc("ConfigService", "SuggestModels")]: (args) => (args[0] ?? []).map((id: string) => {
    const hit = seedConfig.providers[0].models.find((m) => m.id === id);
    const base: any = hit ? { ...hit } : { id, displayName: id };
    // 镜像 config.suggestReasoning
    // 的系列回退：保存时没有表的模型仍会获得级别，因此每回合的选择器保持可见（#
    // 42）。
    if (!base.reasoning && /gpt-5|gpt-6|o3|deepseek|glm|claude|qwen|kimi/.test(id.toLowerCase())) {
      base.reasoning = {
        enabled: true,
        defaultLevel: "high",
        levels: [
          { level: "off", param: "reasoning_effort", value: "none" },
          { level: "low", param: "reasoning_effort", value: "low" },
          { level: "medium", param: "reasoning_effort", value: "medium" },
          { level: "high", param: "reasoning_effort", value: "high" },
          { level: "max", param: "reasoning_effort", value: "max" },
        ],
      };
    }
    return base;
  }),
  // ConfigService.KnowledgeIndexStats
  [svc("ConfigService", "KnowledgeIndexStats")]: () => ({ files: 3, chunks: 12, model: "text-embedding-3-small" }),
  // ConfigService.ListKnowledgeFiles
  [svc("ConfigService", "ListKnowledgeFiles")]: () => [
    { name: "k8s.md", sizeBytes: 4096, modTime: "2026-09-18 01:00" },
    { name: "weekly.md", sizeBytes: 2048, modTime: "2026-09-17 18:30" },
  ],
  // AppService.StorageStats
  [svc("AppService", "StorageStats")]: () => ({ sessionsDbBytes: 524288, knowledgeDbBytes: 65536 }),
  // AppService.MemoryStats
  [svc("AppService", "MemoryStats")]: () => ({ count: 7 }),
  // AppService.SandboxStatus — Settings 平台警告。
  [svc("AppService", "SandboxStatus")]: () => ({
    available: true,
    backend: "windows-appcontainer",
    error: "",
    profileSid: "S-1-15-2-demo",
  }),
  "agentmate/internal/services.AppService.SandboxStatus": () => ({
    available: true,
    backend: "windows-appcontainer",
    error: "",
    profileSid: "S-1-15-2-demo",
  }),
};

(window as any).__lastCalls = [];
(window as any).__seedConfig = seedConfig;
setTransport({
  call: async (_objectID, _method, _windowName, descriptor) => {
    if (_objectID === 5 && _method === 4) {
      return dialogMode === "cancel" ? "" : "/tmp/agentmate-picked";
    }
    (window as any).__lastCalls.push(descriptor);
    // 描述符 {call-id, methodID, args} 作为 args 槽位传入。
    const opt = (descriptor && typeof descriptor === "object"
      ? descriptor
      : { methodID: descriptor, args: [] }) as { methodID?: number | string; methodName?: string; args?: any[] };
    // ByName 调用携带 methodName；ByID 调用（每个生成的绑定）携带
    // methodID。按运行时发送的字段进行路由。
    const h = handlers[opt.methodName ?? String(opt.methodID)];
    if (!h) return null;
    const list = Array.isArray(opt.args) ? opt.args : opt.args == null ? [] : [opt.args];
    // 像真实 IPC 序列化那样对结果进行深拷贝：返回共享引用会让对桩的原地修改绕过
    // React 状态更新（同一引用的 setSessions
    // 会跳过），并显示过期的列表。异步处理器必须先完成——structuredClone
    // 无法克隆 Promise。
    const result = await h(list);
    return result == null ? result : structuredClone(result);
  },
});

// 暴露一个辅助函数，以便测试注入后端事件（chat:stream ...）。
(window as any).__emit = (name: string, data: unknown) => {
  (window as any)._wails?.dispatchWailsEvent?.({ name, data });
};
(window as any).__setTaskSnapshotMode = (mode: TaskSnapshotMode) => {
  taskSnapshotMode = mode;
};

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);

// 视觉回归辅助：?collapse=<project>
// 用于触发关闭状态的项目行（文件夹图标、子项隐藏），无需手动点击。
const collapseProject = stubParams.get("collapse");
if (collapseProject) {
  let attempts = 0;
  const collapseWhenReady = () => {
    const button = [...document.querySelectorAll<HTMLButtonElement>("button[aria-label^='项目「']")]
      .find((candidate) => candidate.getAttribute("aria-label")?.includes(`项目「${collapseProject}」`));
    if (button) {
      button.click();
      return;
    }
    if (attempts++ < 40) window.setTimeout(collapseWhenReady, 100);
  };
  window.setTimeout(collapseWhenReady, 400);
}
