/**
 * isDialogCancelError reports whether a Dialogs.OpenFile failure is just the
 * user closing the native picker. Wails surfaces cancellation as an error
 * ("cancelled by user"), which callers must treat as "nothing selected"
 * instead of a failure toast.
 */
export function isDialogCancelError(err: unknown): boolean {
  const raw = err instanceof Error ? err.message : typeof err === "string" ? err : "";
  const msg = raw.toLowerCase();
  return msg.includes("cancelled by user") || msg.includes("canceled by user");
}

/**
 * pickedDirectoryValue normalizes a native directory picker result. Wails
 * returns the chosen absolute path as a string; an empty (or non-string)
 * value means "nothing selected" and callers must leave the form untouched.
 */
export function pickedDirectoryValue(picked: unknown): string {
  return typeof picked === "string" && picked.trim() !== "" ? picked : "";
}

/**
 * workspaceLockReason explains why the composer's directory menu must not
 * rebind the task workspace. An empty string means the binding may change.
 * Project chats always run in the project root; a started turn locks the
 * directory for everyone.
 */
export function workspaceLockReason(opts: { hasProject: boolean; started: boolean }): string {
  if (opts.started) return "首轮已开始，任务工作区已锁定";
  if (opts.hasProject) return "项目会话使用项目根目录，不能单独指定工作区";
  return "";
}