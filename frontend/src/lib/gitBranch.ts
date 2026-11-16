/**
 * filterGitBranches 在本地缓存的分支列表上做大小写不敏感的子串过滤；
 * 分支搜索不触发额外的 Git 命令。
 */
export function filterGitBranches<T extends { name: string }>(branches: T[], query: string): T[] {
  const q = query.trim().toLowerCase();
  if (!q) return branches;
  return branches.filter((branch) => branch.name.toLowerCase().includes(q));
}

/** dirtyFilesLabel 生成分支行下方的未提交提示。 */
export function dirtyFilesLabel(count: number): string {
  if (count <= 0) return "";
  return count > 99 ? "未提交：99+ 个文件" : `未提交：${count} 个文件`;
}

/** branchDisplayLabel 统一正常分支、分离 HEAD 与不可用状态的文案。 */
export function branchDisplayLabel(branch?: string, detached = false, commit?: string): string {
  if (detached) return commit ? `分离 HEAD · ${commit}` : "分离 HEAD";
  return branch?.trim() || "无 Git 仓库";
}

/**
 * branchSwitchDisabledReason 返回阻止切换分支的原因。
 * 未提交改动允许打开分支菜单并创建新分支，但不能切到另一条已有分支。
 */
export function branchSwitchDisabledReason(info: {
  canSwitch?: boolean;
  dirtyFiles?: number;
  blockedReason?: string;
} | null): string {
  if (!info) return "正在读取分支…";
  if (info.blockedReason) return info.blockedReason;
  if (!info.canSwitch) return "当前工作树不能切换分支";
  if ((info.dirtyFiles ?? 0) > 0) return "请先提交或暂存未提交改动";
  return "";
}
