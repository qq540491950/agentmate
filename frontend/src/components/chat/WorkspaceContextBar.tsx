import { useEffect, useMemo, useRef, useState } from "react";
import {
  Check,
  ChevronDown,
  Folder,
  FolderOpen,
  FolderPlus,
  GitBranch,
  Laptop,
  Loader2,
  Plus,
  Search,
  TriangleAlert,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { TooltipHint } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import {
  Chat,
  errText,
  type GitBranchInfo,
  type GitBranchesInfo,
  type Project,
  type WorkspaceContextInfo,
} from "@/lib/api";
import {
  branchDisplayLabel,
  branchSwitchDisabledReason,
  dirtyFilesLabel,
  filterGitBranches,
} from "@/lib/gitBranch";

type WorkspaceContextBarProps = {
  mode: "draft" | "locked";
  projects: Project[];
  project?: Project | null;
  context?: WorkspaceContextInfo | null;
  contextLoading?: boolean;
  onSelectProject: (projectID: string) => void;
  onChooseDirectory: () => void | Promise<void>;
  onBranchChanged: (context: WorkspaceContextInfo) => void;
  onBranchBusyChange?: (busy: boolean) => void;
};

/**
 * WorkspaceContextBar 在 composer 上方展示并选择本次任务的项目、执行环境和 Git 分支。
 * 草稿态允许选择项目；已有会话的项目只读，分支操作仍受后端仓库锁保护。
 */
export function WorkspaceContextBar({
  mode,
  projects,
  project,
  context,
  contextLoading = false,
  onSelectProject,
  onChooseDirectory,
  onBranchChanged,
  onBranchBusyChange,
}: WorkspaceContextBarProps) {
  const [projectOpen, setProjectOpen] = useState(false);
  const [projectQuery, setProjectQuery] = useState("");
  const [branchOpen, setBranchOpen] = useState(false);
  const [branchQuery, setBranchQuery] = useState("");
  const [branchInfo, setBranchInfo] = useState<GitBranchesInfo | null>(null);
  const [branchLoading, setBranchLoading] = useState(false);
  const [branchError, setBranchError] = useState("");
  const [branchBusy, setBranchBusy] = useState(false);
  const branchRequest = useRef(0);
  const [createOpen, setCreateOpen] = useState(false);
  const [newBranch, setNewBranch] = useState("");
  const [createError, setCreateError] = useState("");

  useEffect(() => {
    branchRequest.current += 1;
    setBranchOpen(false);
    setBranchInfo(null);
    setBranchLoading(false);
    setBranchError("");
    setBranchQuery("");
  }, [project?.id]);

  useEffect(() => {
    onBranchBusyChange?.(branchBusy);
  }, [branchBusy, onBranchBusyChange]);

  const filteredProjects = useMemo(() => {
    const q = projectQuery.trim().toLowerCase();
    if (!q) return projects;
    return projects.filter((item) =>
      item.name.toLowerCase().includes(q) || item.rootDir.toLowerCase().includes(q));
  }, [projectQuery, projects]);

  const loadBranches = async () => {
    if (!project) return;
    const request = ++branchRequest.current;
    setBranchLoading(true);
    setBranchError("");
    try {
      const info = await Chat.GitBranches(project.id);
      if (request === branchRequest.current) setBranchInfo(info);
    } catch (e) {
      if (request === branchRequest.current) {
        setBranchInfo(null);
        setBranchError(errText(e));
      }
    } finally {
      if (request === branchRequest.current) setBranchLoading(false);
    }
  };

  const openBranchPicker = (open: boolean) => {
    setBranchOpen(open);
    if (open) {
      setBranchQuery("");
      void loadBranches();
    }
  };

  const branches = branchInfo?.branches ?? [];
  const filteredBranches = useMemo(
    () => filterGitBranches(branches, branchQuery),
    [branchQuery, branches],
  );
  const switchBlocked = branchSwitchDisabledReason(branchInfo);
  const branchPending = contextLoading || (!!project && !!context && context.projectId !== project.id);
  const branchLabel = branchDisplayLabel(
    context?.gitBranch,
    context?.gitDetached,
    context?.gitCommit,
  );
  const projectLabel = project?.name ?? "选择项目/目录";

  const chooseProject = (projectID: string) => {
    onSelectProject(projectID);
    setProjectOpen(false);
    setProjectQuery("");
  };

  const chooseDirectory = () => {
    setProjectOpen(false);
    setProjectQuery("");
    void onChooseDirectory();
  };

  const switchBranch = async (branch: GitBranchInfo) => {
    if (!project || branch.current || branchBusy) return;
    if (switchBlocked) {
      toast.warning(switchBlocked);
      return;
    }
    setBranchBusy(true);
    try {
      const next = await Chat.SwitchGitBranch(project.id, branch.name);
      onBranchChanged(next);
      setBranchOpen(false);
      toast.success(`已切换到分支「${branch.name}」`);
    } catch (e) {
      toast.error(`切换分支失败: ${errText(e)}`);
      void loadBranches();
    } finally {
      setBranchBusy(false);
    }
  };

  const createBranch = async () => {
    if (!project || !newBranch.trim() || branchBusy) return;
    if (branchInfo?.blockedReason) {
      setCreateError(branchInfo.blockedReason);
      return;
    }
    setBranchBusy(true);
    setCreateError("");
    try {
      const next = await Chat.CreateGitBranch(project.id, newBranch.trim());
      onBranchChanged(next);
      setCreateOpen(false);
      setBranchOpen(false);
      setNewBranch("");
      toast.success(`已创建并切换到分支「${next.gitBranch ?? newBranch.trim()}」`);
    } catch (e) {
      setCreateError(errText(e));
    } finally {
      setBranchBusy(false);
    }
  };

  return (
    <>
      <div className="mb-2 flex min-h-7 flex-wrap items-center gap-1.5">
        {mode === "draft" ? (
          <Popover open={projectOpen} onOpenChange={(open) => {
            setProjectOpen(open);
            if (!open) setProjectQuery("");
          }}>
            <PopoverTrigger asChild>
              <button
                type="button"
                disabled={branchBusy}
                aria-label={project ? `切换项目，当前 ${project.name}` : "选择项目或目录"}
                className="surface-control inline-flex h-7 max-w-[240px] items-center gap-1.5 rounded-lg px-2.5 text-xs text-foreground transition-colors hover:bg-overlay focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring/70 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {project ? <FolderOpen className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /> : <FolderPlus className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                <span className="truncate">{projectLabel}</span>
                <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" aria-hidden="true" />
              </button>
            </PopoverTrigger>
            <PopoverContent align="start" sideOffset={6} className="w-80 p-0">
              <div className="relative p-1.5">
                <Search className="pointer-events-none absolute left-3.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={projectQuery}
                  onChange={(e) => setProjectQuery(e.target.value)}
                  placeholder="搜索项目"
                  aria-label="搜索项目"
                  autoFocus
                  className="h-8 rounded-md bg-control pl-8 text-xs"
                />
              </div>
              <div className="max-h-64 overflow-y-auto px-1.5" role="listbox" aria-label="项目列表">
                <div className="px-2 pb-1 pt-1.5 text-xs font-medium text-muted-foreground">项目</div>
                {filteredProjects.map((item) => {
                  const selected = item.id === project?.id;
                  return (
                    <button
                      key={item.id}
                      type="button"
                      role="option"
                      aria-selected={selected}
                      onClick={() => chooseProject(item.id)}
                      className={cn(
                        "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-control",
                        selected && "bg-selected text-foreground",
                      )}
                    >
                      <Folder className="h-3.5 w-3.5 shrink-0" />
                      <span className="min-w-0 flex-1 truncate">{item.name}</span>
                      {selected && <Check className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
                    </button>
                  );
                })}
                {filteredProjects.length === 0 && (
                  <div className="px-2 py-3 text-center text-xs text-muted-foreground">没有匹配的项目</div>
                )}
              </div>
              <div className="mt-1 p-1.5 pt-0">
                <button
                  type="button"
                  onClick={chooseDirectory}
                  className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-control"
                >
                  <FolderPlus className="h-3.5 w-3.5 shrink-0" />
                  选择其他目录…
                </button>
                <button
                  type="button"
                  onClick={() => chooseProject("")}
                  className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-muted-foreground transition-colors hover:bg-control hover:text-foreground"
                >
                  <Folder className="h-3.5 w-3.5 shrink-0" />
                  不在项目中工作
                </button>
              </div>
            </PopoverContent>
          </Popover>
        ) : (
          <TooltipHint content={context?.workspaceDir || "默认项目"}>
            <span className="surface-control inline-flex h-7 max-w-[240px] items-center gap-1.5 rounded-lg px-2.5 text-xs text-muted-foreground">
              <FolderOpen className="h-3.5 w-3.5 shrink-0" />
              <span className="truncate">{project?.name ?? "默认项目"}</span>
            </span>
          </TooltipHint>
        )}

        <TooltipHint content="任务在本机执行">
          <span className="surface-control inline-flex h-7 items-center gap-1.5 rounded-lg px-2.5 text-xs text-muted-foreground">
            <Laptop className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
            本地
          </span>
        </TooltipHint>

        {project && (
          branchPending ? (
            <span className="surface-control inline-flex h-7 items-center gap-1.5 rounded-lg px-2.5 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
              读取分支…
            </span>
          ) : context?.gitRepoRoot ? (
            <Popover open={branchOpen} onOpenChange={openBranchPicker}>
              <TooltipHint
                content={branchOpen
                  ? null
                  : context.gitDirtyFiles
                    ? `${branchLabel}\n${dirtyFilesLabel(context.gitDirtyFiles)}`
                    : branchLabel}
                wrap
              >
                <PopoverTrigger asChild>
                  <button
                    type="button"
                    aria-label={`切换 Git 分支，当前 ${branchLabel}`}
                    className="surface-control inline-flex h-7 max-w-[220px] items-center gap-1.5 rounded-lg px-2.5 text-xs text-foreground transition-colors hover:bg-overlay focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring/70"
                  >
                    {branchBusy ? <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin" /> : <GitBranch className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                    <span className="truncate">{branchBusy ? "切换中…" : branchLabel}</span>
                    {!branchBusy && <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" aria-hidden="true" />}
                  </button>
                </PopoverTrigger>
              </TooltipHint>
              <PopoverContent align="start" sideOffset={6} className="w-80 p-0">
                <div className="relative p-1.5">
                  <Search className="pointer-events-none absolute left-3.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    value={branchQuery}
                    onChange={(e) => setBranchQuery(e.target.value)}
                    placeholder={`搜索${project.name}分支`}
                    aria-label={`搜索${project.name}分支`}
                    autoFocus
                    className="h-8 rounded-md bg-control pl-8 text-xs"
                  />
                </div>
                {branchInfo?.blockedReason && (
                  <div className="mx-1.5 mb-1 flex items-start gap-1.5 rounded-md bg-warning-soft px-2 py-1.5 text-xs leading-relaxed text-warning-text">
                    <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                    <span>{branchInfo.blockedReason}</span>
                  </div>
                )}
                {!branchInfo?.blockedReason && (branchInfo?.dirtyFiles ?? 0) > 0 && (
                  <div className="mx-1.5 mb-1 flex items-start gap-1.5 rounded-md bg-warning-soft px-2 py-1.5 text-xs leading-relaxed text-warning-text">
                    <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                    <span>{dirtyFilesLabel(branchInfo?.dirtyFiles ?? 0)}。切换已有分支前请先提交或暂存；创建新分支会保留这些改动。</span>
                  </div>
                )}
                <div className="max-h-64 overflow-y-auto px-1.5" role="listbox" aria-label="分支列表">
                  <div className="flex items-center justify-between px-2 pb-1 pt-1.5">
                    <span className="text-xs font-medium text-muted-foreground">分支</span>
                    {branchInfo?.detached && (
                      <span className="truncate text-xs text-muted-foreground">
                        {branchDisplayLabel("", true, branchInfo.headCommit)}
                      </span>
                    )}
                  </div>
                  {branchLoading && (
                    <div className="flex items-center justify-center gap-2 py-6 text-xs text-muted-foreground">
                      <Loader2 className="h-3.5 w-3.5 animate-spin" /> 正在读取分支…
                    </div>
                  )}
                  {!branchLoading && branchError && (
                    <div className="space-y-2 px-2 py-4 text-center text-xs text-danger-text">
                      <div>{branchError}</div>
                      <button type="button" className="underline underline-offset-2" onClick={() => void loadBranches()}>
                        重试
                      </button>
                    </div>
                  )}
                  {!branchLoading && !branchError && filteredBranches.map((branch) => {
                    const disabled = !branch.current && !!switchBlocked;
                    const dirty = branch.current && (branchInfo?.dirtyFiles ?? 0) > 0;
                    return (
                      <button
                        key={branch.name}
                        type="button"
                        role="option"
                        aria-selected={branch.current}
                        aria-disabled={disabled}
                        disabled={disabled}
                        onClick={() => void switchBranch(branch)}
                        className={cn(
                          "flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-control disabled:cursor-not-allowed disabled:opacity-50",
                          branch.current && "bg-selected text-foreground",
                        )}
                      >
                        <GitBranch className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate font-medium">{branch.name}</span>
                          {dirty && <span className="mt-0.5 block text-xs text-muted-foreground">{dirtyFilesLabel(branchInfo?.dirtyFiles ?? 0)}</span>}
                        </span>
                        {branch.current && <Check className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
                      </button>
                    );
                  })}
                  {!branchLoading && !branchError && filteredBranches.length === 0 && (
                    <div className="px-2 py-4 text-center text-xs text-muted-foreground">没有匹配的分支</div>
                  )}
                </div>
                <div className="mt-1 p-1.5 pt-0">
                  <button
                    type="button"
                    disabled={!branchInfo || !!branchInfo.blockedReason || branchLoading || branchBusy}
                    onClick={() => {
                      setCreateError("");
                      setNewBranch("");
                      setCreateOpen(true);
                    }}
                    className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-control disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    <Plus className="h-3.5 w-3.5 shrink-0" />
                    创建并检出分支…
                  </button>
                </div>
              </PopoverContent>
            </Popover>
          ) : (
            <span className="surface-control inline-flex h-7 items-center gap-1.5 rounded-lg px-2.5 text-xs text-muted-foreground">
              <GitBranch className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              无 Git 仓库
            </span>
          )
        )}
      </div>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>创建并检出分支</DialogTitle>
            <DialogDescription>
              从当前分支「{branchDisplayLabel(context?.gitBranch, context?.gitDetached, context?.gitCommit)}」创建，新分支会立即检出。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <label htmlFor="new-branch-name" className="text-xs font-medium text-muted-foreground">分支名</label>
            <Input
              id="new-branch-name"
              value={newBranch}
              onChange={(e) => {
                setNewBranch(e.target.value);
                setCreateError("");
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  void createBranch();
                }
              }}
              placeholder="例如 feature/login"
              autoFocus
            />
            {(branchInfo?.dirtyFiles ?? 0) > 0 && (
              <p className="text-xs leading-relaxed text-warning-text">
                当前有 {branchInfo?.dirtyFiles} 个未提交文件，这些改动会保留在新分支。
              </p>
            )}
            {createError && <p className="text-xs leading-relaxed text-danger-text">{createError}</p>}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>取消</Button>
            <Button onClick={() => void createBranch()} disabled={!newBranch.trim() || branchBusy}>
              {branchBusy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              创建并检出
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
