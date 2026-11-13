import { useCallback, useEffect, useRef, useState } from "react";
import { Dialogs } from "@wailsio/runtime";
import { Plus, Trash2, Sparkles, FolderDown, ListTodo, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/EmptyState";
import { TooltipHint } from "@/components/ui/tooltip";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { SegmentedControl } from "@/components/ui/choice";
import { Badge } from "@/components/ui/badge";
import { Chat, Config, type AppConfig, type Project, type SkillConfig, errText } from "@/lib/api";
import { isReadOnlySkill, skillSource, skillSourceLabel } from "@/lib/skills";
import { isDialogCancelError, pickedDirectoryValue } from "@/lib/workspace";
import { PageHeader, PageShell } from "@/components/layout/PageShell";

type SkillScope = "global" | "project";

// Radix Select 不接受空字符串作为受控值；默认工作区用哨兵值映射回空 projectID。
const DEFAULT_WORKSPACE = "__default_workspace";

function blankSkill(): SkillConfig {
  return { id: "", name: "", description: "", content: "", createdAt: "" };
}

function skillPreview(content: string): string {
  return content.length > 200 ? content.slice(0, 200) + "…" : content;
}

export function SkillsPage() {
  const [cfg, setCfg] = useState<AppConfig | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [scope, setScope] = useState<SkillScope>("global");
  const [projectID, setProjectID] = useState("");
  const [projectSkills, setProjectSkills] = useState<SkillConfig[]>([]);
  const [projectSkillsLoading, setProjectSkillsLoading] = useState(false);
  const [editing, setEditing] = useState<SkillConfig | null>(null);
  const projectSkillsRequest = useRef(0);

  const [importOpen, setImportOpen] = useState(false);
  const [importScope, setImportScope] = useState<SkillScope>("global");
  const [importProjectID, setImportProjectID] = useState("");
  const [zipPath, setZipPath] = useState("");
  const [importing, setImporting] = useState(false);

  const reload = useCallback(() => {
    Config.GetConfig()
      .then(setCfg)
      .catch((e) => toast.error(`加载 Skills 失败: ${errText(e)}`));
    Chat.ListProjects()
      .then((list) => setProjects(list ?? []))
      .catch((e) => toast.error(`加载项目失败: ${errText(e)}`));
  }, []);
  useEffect(reload, [reload]);

  const reloadProjectSkills = useCallback((pid: string) => {
    const request = ++projectSkillsRequest.current;
    setProjectSkillsLoading(true);
    setProjectSkills([]);
    Chat.EffectiveSkills("", pid)
      .then((list) => {
        if (request !== projectSkillsRequest.current) return;
        setProjectSkills((list ?? []).filter((skill) => skillSource(skill.id) === "project"));
      })
      .catch((e) => {
        if (request === projectSkillsRequest.current) toast.error(`加载项目 Skills 失败: ${errText(e)}`);
      })
      .finally(() => {
        if (request === projectSkillsRequest.current) setProjectSkillsLoading(false);
      });
  }, []);

  useEffect(() => {
    if (scope === "project") reloadProjectSkills(projectID);
  }, [scope, projectID, reloadProjectSkills]);

  const workspaceDir = cfg?.settings?.workspaceDir ?? "";
  useEffect(() => {
    // 没有默认工作区时，项目标签默认选中第一个已注册项目，避免停在不可用的
    // “默认工作区”选项上。
    if (scope === "project" && !projectID && !workspaceDir && projects.length > 0) {
      setProjectID(projects[0].id);
    }
  }, [scope, projectID, workspaceDir, projects]);

  const selectedProject = projects.find((project) => project.id === projectID);
  const activeProjectRoot = selectedProject?.rootDir || workspaceDir;
  const skills = cfg?.skills ?? [];
  const globalSkills = skills.filter((skill) => skillSource(skill.id) !== "project");
  const visibleSkills = scope === "global" ? globalSkills : projectSkills;

  const importProject = projects.find((project) => project.id === importProjectID);
  const importProjectRoot = importProject?.rootDir || workspaceDir;
  const importProjectAvailable = projects.length > 0 || !!workspaceDir;

  const save = async () => {
    if (!editing) return;
    if (!editing.name.trim()) {
      toast.error("请填写 Skill 名称（建议英文短横线命名，如 weekly-report）");
      return;
    }
    try {
      await Config.SaveSkill(editing);
      toast.success("已保存");
      setEditing(null);
      reload();
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    }
  };

  const remove = async (s: SkillConfig) => {
    if (!window.confirm(`确定删除 Skill「${s.name}」？`)) return;
    try {
      await Config.DeleteSkill(s.id);
      toast.success(`已删除 ${s.name}`);
    } catch (e) {
      toast.error(`删除失败: ${errText(e)}`);
    }
    reload();
  };

  const openImport = () => {
    setImportScope(scope);
    setImportProjectID(scope === "project" ? (projectID || (workspaceDir ? "" : projects[0]?.id ?? "")) : "");
    setZipPath("");
    setImportOpen(true);
  };

  const pickSkillZip = async () => {
    try {
      const picked = await Dialogs.OpenFile({
        CanChooseFiles: true,
        CanChooseDirectories: false,
        ResolvesAliases: true,
        Filters: [{ DisplayName: "Skill ZIP", Pattern: "*.zip" }],
        Title: "选择 Skill 压缩包",
        ButtonText: "选择",
      });
      const path = pickedDirectoryValue(picked);
      if (path) setZipPath(path);
    } catch (e) {
      if (isDialogCancelError(e)) return;
      toast.error(`选择文件失败: ${errText(e)}`);
    }
  };

  const importSkill = async () => {
    if (!zipPath) {
      toast.error("请选择 Skill ZIP 压缩包");
      return;
    }
    if (importScope === "project" && !importProjectAvailable) {
      toast.error("请先在设置中配置工作区目录，或添加一个项目");
      return;
    }
    setImporting(true);
    try {
      const targetPID = importScope === "project" ? importProjectID : "";
      const result = await Config.ImportSkillZip(zipPath, importScope, targetPID);
      const targetLabel = importScope === "global"
        ? "全局"
        : importProject?.name || "默认工作区";
      toast.success(`已导入 Skill「${result.name}」到${targetLabel}`);
      setImportOpen(false);
      if (importScope === "project") {
        setScope("project");
        setProjectID(importProjectID);
        reloadProjectSkills(importProjectID);
      } else {
        setScope("global");
      }
      reload();
    } catch (e) {
      toast.error(`导入失败: ${errText(e)}`);
    } finally {
      setImporting(false);
    }
  };

  return (
    <PageShell>
      <PageHeader
        title="Skills"
        description="可复用的技能指令包（SKILL.md 规范），可安装到全局或指定项目"
        actions={
          <>
            <TooltipHint content="导入内含 SKILL.md 的 ZIP 包；可选择安装到全局或项目">
              <Button variant="outline" onClick={openImport}>
                <FolderDown className="h-4 w-4" /> 导入 Skill
              </Button>
            </TooltipHint>
            {scope === "global" && (
              <Button onClick={() => setEditing(blankSkill())}>
                <Plus className="h-4 w-4" /> 新增 Skill
              </Button>
            )}
          </>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <SegmentedControl<SkillScope>
          value={scope}
          onChange={setScope}
          ariaLabel="Skill 范围"
          options={[
            { value: "global", label: "全局" },
            { value: "project", label: "项目" },
          ]}
        />
        {scope === "project" && (
          <Select
            value={projectID || DEFAULT_WORKSPACE}
            onValueChange={(value) => setProjectID(value === DEFAULT_WORKSPACE ? "" : value)}
          >
            <SelectTrigger className="w-56"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={DEFAULT_WORKSPACE} disabled={!workspaceDir}>默认工作区</SelectItem>
              {projects.map((project) => (
                <SelectItem key={project.id} value={project.id}>{project.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
          {scope === "global"
            ? "应用内 Skill 与 ~/.agents/skills（全局只读）"
            : activeProjectRoot
              ? `安装目录：${activeProjectRoot}/.agents/skills`
              : "尚未配置工作区或项目"}
        </span>
      </div>

      <div className="grid gap-1.5 rounded-2xl bg-panel/40 p-1.5">
        {scope === "project" && projectSkillsLoading && (
          <Card>
            <div className="flex items-center gap-2 px-4 py-3 text-sm text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" /> 正在加载项目 Skills…
            </div>
          </Card>
        )}
        {visibleSkills.length === 0 && !(scope === "project" && projectSkillsLoading) && (
          <Card>
            <EmptyState
              icon={ListTodo}
              title={scope === "global" ? "还没有 Skill" : "该项目还没有 Skill"}
              desc={scope === "global"
                ? "点击「新增 Skill」创建，或导入包含 SKILL.md 的 ZIP 包。"
                : "点击「导入 Skill」，把 ZIP 包安装到项目的 .agents/skills 目录。"}
            />
          </Card>
        )}
        {visibleSkills.map((s) => {
          const source = skillSource(s.id);
          const readOnly = isReadOnlySkill(s.id);
          return (
            <div key={s.id} className="flex items-start gap-3.5 rounded-xl bg-panel/70 px-4 py-3.5">
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-accent-soft">
                <Sparkles className="h-4 w-4 text-accent-text" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium">{s.name}</span>
                  <Badge variant="secondary" className="font-normal">{skillSourceLabel(source)}</Badge>
                </div>
                <div className="mt-0.5 text-xs text-muted-foreground">{s.description}</div>
                <pre className="mt-2 max-h-20 overflow-hidden whitespace-pre-wrap text-xs text-muted-foreground">
                  {skillPreview(s.content)}
                </pre>
              </div>
              <div className="flex shrink-0 items-center gap-1">
                {readOnly ? (
                  <TooltipHint content={source === "global"
                    ? "来自 ~/.agents/skills，修改请编辑对应目录的 SKILL.md"
                    : "来自项目 .agents/skills，修改请编辑对应目录的 SKILL.md"}>
                    <span className="text-xs text-muted-foreground">只读</span>
                  </TooltipHint>
                ) : (
                  <>
                    <Button variant="outline" size="sm" onClick={() => setEditing({ ...s })}>编辑</Button>
                    <TooltipHint content="删除 Skill">
                      <Button variant="ghost" size="icon" aria-label="删除 Skill" onClick={() => remove(s)}>
                        <Trash2 className="h-4 w-4 text-muted-foreground" />
                      </Button>
                    </TooltipHint>
                  </>
                )}
              </div>
            </div>
          );
        })}
      </div>

      <Dialog open={importOpen} onOpenChange={(open) => !importing && setImportOpen(open)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>导入 Skill</DialogTitle>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="grid gap-1.5">
              <Label>导入到</Label>
              <SegmentedControl<SkillScope>
                value={importScope}
                onChange={setImportScope}
                ariaLabel="Skill 导入范围"
                options={[
                  { value: "global", label: "全局" },
                  { value: "project", label: "项目", disabled: !importProjectAvailable },
                ]}
              />
              <p className="text-xs text-muted-foreground">
                {importScope === "global"
                  ? "安装到 ~/.agents/skills，所有项目与 Agent 都可使用。"
                  : "安装到所选项目的 .agents/skills，仅在该项目的会话中可用。"}
              </p>
            </div>

            {importScope === "project" && (
              <div className="grid gap-1.5">
                <Label>项目</Label>
                <Select
                  value={importProjectID || DEFAULT_WORKSPACE}
                  onValueChange={(value) => setImportProjectID(value === DEFAULT_WORKSPACE ? "" : value)}
                >
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value={DEFAULT_WORKSPACE} disabled={!workspaceDir}>默认工作区</SelectItem>
                    {projects.map((project) => (
                      <SelectItem key={project.id} value={project.id}>{project.name}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <p className="truncate text-xs text-muted-foreground">
                  {importProjectRoot ? `${importProjectRoot}/.agents/skills` : "请先在设置中配置工作区或添加项目"}
                </p>
              </div>
            )}

            <div className="grid gap-1.5">
              <Label>Skill 压缩包</Label>
              <div className="flex items-center gap-2">
                <div className="min-w-0 flex-1 truncate rounded-lg bg-control px-3 py-2 text-xs text-muted-foreground">
                  {zipPath || "尚未选择 ZIP 文件"}
                </div>
                <Button variant="outline" onClick={pickSkillZip}>选择文件</Button>
              </div>
              <p className="text-xs leading-relaxed text-muted-foreground">
                ZIP 内需包含 SKILL.md（YAML frontmatter 必须提供合法的 name 与 description），可位于根目录或单层文件夹；references/、assets/、scripts/ 会一并安装。
              </p>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setImportOpen(false)} disabled={importing}>取消</Button>
            <Button onClick={importSkill} disabled={importing || !zipPath}>
              {importing && <Loader2 className="h-4 w-4 animate-spin" />} {importing ? "导入中…" : "导入"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!editing} onOpenChange={(v) => !v && setEditing(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{editing?.id ? "编辑 Skill" : "新增 Skill"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <div className="grid gap-4 py-2">
              <div className="grid grid-cols-2 gap-3">
                <div className="grid gap-1.5">
                  <Label>名称（英文标识）</Label>
                  <Input
                    value={editing.name}
                    onChange={(e) => setEditing({ ...editing, name: e.target.value })}
                    placeholder="weekly-report"
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label>描述</Label>
                  <Input
                    value={editing.description}
                    onChange={(e) => setEditing({ ...editing, description: e.target.value })}
                    placeholder="何时使用该技能"
                  />
                </div>
              </div>
              <div className="grid gap-1.5">
                <Label>技能内容（Markdown）</Label>
                <Textarea
                  rows={12}
                  className="font-mono text-xs"
                  value={editing.content}
                  onChange={(e) => setEditing({ ...editing, content: e.target.value })}
                  placeholder="# 技能说明&#10;&#10;按照以下步骤……"
                />
              </div>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)}>取消</Button>
            <Button onClick={save}>保存</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </PageShell>
  );
}
