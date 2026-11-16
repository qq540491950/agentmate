/**
 * isDialogCancelError 报告 Dialogs.OpenFile
 * 失败是否只是用户关闭了原生选择器。Wails 会将取消作为错误（"cancelled by
 * user"）暴露，调用方必须将其视为 "nothing selected"，而不是失败提示。
 */
export function isDialogCancelError(err: unknown): boolean {
  const raw = err instanceof Error ? err.message : typeof err === "string" ? err : "";
  const msg = raw.toLowerCase();
  return msg.includes("cancelled by user") || msg.includes("canceled by user");
}

/**
 * pickedDirectoryValue 会规范化原生目录选择器的结果。Wails
 * 以字符串返回所选的绝对路径；空值（或非字符串值）表示 "nothing
 * selected"，调用方必须保持表单不变。
 */
export function pickedDirectoryValue(picked: unknown): string {
  return typeof picked === "string" && picked.trim() !== "" ? picked : "";
}

/**
 * normalizeWorkspacePath 以与侧栏项目分组一致的方式比较目录：
 * 统一分隔符、去掉尾斜杠，并按 Windows 习惯忽略大小写。
 */
export function normalizeWorkspacePath(path: string): string {
  return path.replace(/\\/g, "/").replace(/\/+$/, "").toLowerCase();
}

/** findProjectByRoot 在已注册项目中查找目录对应的项目。 */
export function findProjectByRoot<T extends { rootDir: string }>(projects: T[], directory: string): T | undefined {
  const target = normalizeWorkspacePath(directory);
  if (!target) return undefined;
  return projects.find((project) => normalizeWorkspacePath(project.rootDir) === target);
}

/**
 * workspaceLockReason
 * 说明为什么输入区的目录菜单不得重新绑定任务工作区。空字符串表示绑定可以更改。
 * 项目会话始终在项目根目录中运行；已开始的回合会为所有人锁定该目录。
 */
export function workspaceLockReason(opts: { hasProject: boolean; started: boolean }): string {
  if (opts.started) return "首轮已开始，任务工作区已锁定";
  if (opts.hasProject) return "项目会话使用项目根目录，不能单独指定工作区";
  return "";
}
