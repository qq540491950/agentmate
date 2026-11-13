/**
 * shouldOpenLatestSession 决定 ChatPage
 * 是否可以在列表加载或变化后自动选中最新会话。现在点击「新聊天」会打开一个未保
 * 存的草稿（activeID
 * 为空，且草稿标记已设置）：自动选中旧会话会替换掉空白的输入框，而急切地调用
 * NewSession 会在侧边栏留下一个空行（issue #30）。
 */
export function shouldOpenLatestSession(opts: {
  activeID: string;
  draft: boolean;
  sessionCount: number;
}): boolean {
  return opts.activeID === "" && !opts.draft && opts.sessionCount > 0;
}

/**
 * draftUsesProject 决定由哪个 RPC
 * 为尚未保存的聊天创建会话：从项目的「+」打开的草稿绑定到该项目，而全局「新聊天
 * 」则创建一个独立的任务会话。两者都不会持久化任何行，直到第一条消息真正发送（i
 * ssue #60：此前每次点击都会创建一个空的「新对话」）。
 */
export function draftUsesProject(projectID: string): boolean {
  return projectID.trim() !== "";
}

/**
 * draftModelFor
 * 解析尚未创建的聊天将要运行的模型。输入框的选择器必须在第一条消息存在之前就保
 * 持可用（issue
 * #64），因此当该模型仍然存在时，它显示显式的草稿选择，否则显示供应商的第一个模
 * 型，这与后端对不再存在于供应商列表中的覆盖项所采用的回退一致。列表为空意味着
 * 未配置任何供应商，选择器保持隐藏。
 */
export function draftModelFor<T extends { id: string }>(models: T[], override: string): T | null {
  if (models.length === 0) return null;
  return models.find((m) => m.id === override) ?? models[0];
}
