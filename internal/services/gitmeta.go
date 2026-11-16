package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"agentmate/internal/config"
)

const (
	gitReadTimeout   = 2 * time.Second
	gitStatusTimeout = 8 * time.Second
	gitSwitchTimeout = 2 * time.Minute
)

// GitBranchInfo 是分支选择器中的一行。
type GitBranchInfo struct {
	Name    string `json:"name"`
	Current bool   `json:"current,omitempty"`
}

// GitBranchesInfo 描述一个项目工作树的 Git 分支状态。
type GitBranchesInfo struct {
	ProjectID     string          `json:"projectId"`
	RepoRoot      string          `json:"repoRoot,omitempty"`
	Current       string          `json:"current,omitempty"`
	Detached      bool            `json:"detached,omitempty"`
	HeadCommit    string          `json:"headCommit,omitempty"`
	DirtyFiles    int             `json:"dirtyFiles,omitempty"`
	DirtyKnown    bool            `json:"dirtyKnown,omitempty"`
	Branches      []GitBranchInfo `json:"branches"`
	CanSwitch     bool            `json:"canSwitch"`
	BlockedReason string          `json:"blockedReason,omitempty"`
}

// WorkspaceContextInfo 是 composer 工作区上下文条的数据源。
type WorkspaceContextInfo struct {
	ProjectID     string `json:"projectId,omitempty"`
	ProjectName   string `json:"projectName,omitempty"`
	WorkspaceDir  string `json:"workspaceDir"`
	GitRepoRoot   string `json:"gitRepoRoot,omitempty"`
	GitBranch     string `json:"gitBranch,omitempty"`
	GitDetached   bool   `json:"gitDetached,omitempty"`
	GitCommit     string `json:"gitCommit,omitempty"`
	GitDirtyFiles int    `json:"gitDirtyFiles,omitempty"`
}

// WorkspaceContext 解析会话或草稿项目的工作区，并尽力附带 Git 元数据。
// Git 不可用绝不能阻断聊天；此时只返回空的分支字段。
func (c *ChatService) WorkspaceContext(sessionID, projectID string) (WorkspaceContextInfo, error) {
	cfg := c.S.Store.Get()
	info := WorkspaceContextInfo{}
	workspace := ""
	if strings.TrimSpace(sessionID) != "" {
		var sess *config.ChatSession
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ID == sessionID {
				sess = &cfg.Sessions[i]
				break
			}
		}
		if sess == nil {
			return info, fmt.Errorf("会话 %q 不存在", sessionID)
		}
		workspace = sess.EffectiveWorkspaceFor(cfg)
		if project, err := findProjectByID(cfg, sess.ProjectID); err == nil {
			info.ProjectID = project.ID
			info.ProjectName = project.Name
		}
	} else if strings.TrimSpace(projectID) != "" {
		project, err := findProjectByID(cfg, projectID)
		if err != nil {
			return info, err
		}
		info.ProjectID = project.ID
		info.ProjectName = project.Name
		workspace = project.RootDir
	} else {
		workspace = cfg.Settings.WorkspaceDir
	}
	info.WorkspaceDir = strings.TrimSpace(workspace)
	if info.WorkspaceDir == "" {
		return info, nil
	}

	ctx := context.Background()
	repoRoot, err := c.gitRepoRoot(ctx, info.WorkspaceDir)
	if err != nil {
		return info, nil
	}
	info.GitRepoRoot = repoRoot
	branch, commit, detached, err := gitCurrentBranch(ctx, repoRoot)
	if err == nil {
		info.GitBranch = branch
		info.GitDetached = detached
		info.GitCommit = commit
	}
	if dirty, err := gitDirtyCount(ctx, repoRoot); err == nil {
		info.GitDirtyFiles = dirty
	}
	return info, nil
}

// GitBranches 列出项目仓库的本地分支与当前工作树状态。
func (c *ChatService) GitBranches(projectID string) (GitBranchesInfo, error) {
	cfg := c.S.Store.Get()
	project, err := findProjectByID(cfg, projectID)
	if err != nil {
		return GitBranchesInfo{}, err
	}
	info := GitBranchesInfo{ProjectID: project.ID, Branches: []GitBranchInfo{}}
	ctx := context.Background()
	repoRoot, err := c.gitRepoRoot(ctx, project.RootDir)
	if err != nil {
		info.BlockedReason = gitBlockedReason(err)
		return info, nil
	}
	info.RepoRoot = repoRoot
	current, commit, detached, err := gitCurrentBranch(ctx, repoRoot)
	if err != nil {
		return info, fmt.Errorf("读取当前分支失败: %w", err)
	}
	info.Current = current
	info.Detached = detached
	info.HeadCommit = commit
	if dirty, err := gitDirtyCount(ctx, repoRoot); err == nil {
		info.DirtyFiles = dirty
		info.DirtyKnown = true
	}
	branches, err := gitListBranches(ctx, repoRoot, current)
	if err != nil {
		return info, fmt.Errorf("读取分支列表失败: %w", err)
	}
	info.Branches = branches
	switch {
	case !samePath(repoRoot, project.RootDir):
		info.BlockedReason = "项目目录不是 Git 仓库根目录，不能切换分支"
	case !c.repoIsIdle(repoRoot):
		info.BlockedReason = "项目内有会话正在运行，暂不能切换分支"
	default:
		info.CanSwitch = true
	}
	return info, nil
}

// SwitchGitBranch 在干净且空闲的项目工作树上切换到已有本地分支。
func (c *ChatService) SwitchGitBranch(projectID, branch string) (WorkspaceContextInfo, error) {
	project, repoRoot, release, err := c.lockProjectForBranchChange(projectID)
	if err != nil {
		return WorkspaceContextInfo{}, err
	}
	defer release()

	ctx := context.Background()
	branch = strings.TrimSpace(branch)
	if err := validateBranchName(ctx, repoRoot, branch); err != nil {
		return WorkspaceContextInfo{}, err
	}
	from, _, detached, err := gitCurrentBranch(ctx, repoRoot)
	if err != nil {
		return WorkspaceContextInfo{}, fmt.Errorf("读取当前分支失败: %w", err)
	}
	if !detached && from == branch {
		return c.WorkspaceContext("", project.ID)
	}
	if !gitLocalBranchExists(ctx, repoRoot, branch) {
		return WorkspaceContextInfo{}, fmt.Errorf("分支「%s」不存在", branch)
	}
	dirty, err := gitDirtyCount(ctx, repoRoot)
	if err != nil {
		return WorkspaceContextInfo{}, fmt.Errorf("检查未提交改动失败: %w", err)
	}
	if dirty > 0 {
		return WorkspaceContextInfo{}, fmt.Errorf("当前有 %d 个未提交文件，请先提交或暂存后再切换分支", dirty)
	}

	// 只使用 switch，不做 force checkout、reset、clean 或自动 stash。
	if _, err := runGitWrite(ctx, repoRoot, "switch", "--no-guess", branch); err != nil {
		return WorkspaceContextInfo{}, fmt.Errorf("切换分支失败: %w", err)
	}
	c.resetProjectBaselines(project.ID)
	c.audit("projects", "project_branch_switched", "user", "project", mustJSON(map[string]string{
		"projectId": project.ID,
		"repoRoot":  repoRoot,
		"from":      from,
		"to":        branch,
	}), "")
	return c.WorkspaceContext("", project.ID)
}

// CreateGitBranch 从当前 HEAD 创建并检出分支。未提交改动会保留在工作树中。
func (c *ChatService) CreateGitBranch(projectID, name string) (WorkspaceContextInfo, error) {
	project, repoRoot, release, err := c.lockProjectForBranchChange(projectID)
	if err != nil {
		return WorkspaceContextInfo{}, err
	}
	defer release()

	ctx := context.Background()
	name = strings.TrimSpace(name)
	if err := validateBranchName(ctx, repoRoot, name); err != nil {
		return WorkspaceContextInfo{}, err
	}
	if gitLocalBranchExists(ctx, repoRoot, name) {
		return WorkspaceContextInfo{}, fmt.Errorf("分支「%s」已存在", name)
	}
	from, commit, detached, err := gitCurrentBranch(ctx, repoRoot)
	if err != nil {
		return WorkspaceContextInfo{}, fmt.Errorf("读取当前分支失败: %w", err)
	}
	if _, err := runGitWrite(ctx, repoRoot, "switch", "-c", name); err != nil {
		return WorkspaceContextInfo{}, fmt.Errorf("创建分支失败: %w", err)
	}
	base := from
	if detached {
		base = commit
	}
	c.audit("projects", "project_branch_created", "user", "project", mustJSON(map[string]string{
		"projectId": project.ID,
		"repoRoot":  repoRoot,
		"base":      base,
		"branch":    name,
	}), "")
	return c.WorkspaceContext("", project.ID)
}

func (c *ChatService) lockProjectForBranchChange(projectID string) (config.Project, string, func(), error) {
	cfg := c.S.Store.Get()
	project, err := findProjectByID(cfg, projectID)
	if err != nil {
		return config.Project{}, "", func() {}, err
	}
	repoRoot, err := c.gitRepoRoot(context.Background(), project.RootDir)
	if err != nil {
		return config.Project{}, "", func() {}, fmt.Errorf("%s", gitBlockedReason(err))
	}
	if !samePath(repoRoot, project.RootDir) {
		return config.Project{}, "", func() {}, fmt.Errorf("项目目录不是 Git 仓库根目录，不能切换分支")
	}
	lock := c.repoLock(repoRoot)
	if !lock.TryLock() {
		return config.Project{}, "", func() {}, fmt.Errorf("项目内有会话正在运行，暂不能切换分支")
	}
	// 分支检出与任务基线重建都必须避开并发会话创建工作区。
	c.workspaceMu.Lock()
	release := func() {
		c.workspaceMu.Unlock()
		lock.Unlock()
	}
	return project, repoRoot, release, nil
}

func (c *ChatService) resetProjectBaselines(projectID string) {
	cfg := c.S.Store.Get()
	for _, sess := range cfg.Sessions {
		if sess.ProjectID != projectID {
			continue
		}
		if _, err := resetTaskBaseline(c.S.Store, sess.ID, sess.EffectiveWorkspaceFor(cfg)); err != nil {
			log.Printf("分支切换后重建会话 %s 的任务基线失败: %v", sess.ID, err)
		}
	}
}

func findProjectByID(cfg config.Config, projectID string) (config.Project, error) {
	if strings.TrimSpace(projectID) == "" {
		return config.Project{}, fmt.Errorf("请选择项目")
	}
	for _, project := range cfg.Projects {
		if project.ID == projectID {
			return project, nil
		}
	}
	return config.Project{}, fmt.Errorf("项目不存在或已被删除")
}

func (c *ChatService) gitRepoRoot(ctx context.Context, workspace string) (string, error) {
	key := repoKey(workspace)
	if cached, ok := c.repoRoots.Load(key); ok {
		if root, _ := cached.(string); root != "" {
			return root, nil
		}
	}
	out, err := runGitRead(ctx, workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := filepath.Clean(strings.TrimSpace(out))
	if root == "" || root == "." {
		return "", fmt.Errorf("无法解析 Git 仓库根目录")
	}
	c.repoRoots.Store(key, root)
	return root, nil
}

// lockRepoForTurn 为项目会话回合持有仓库读锁，使分支检出不会与工具执行交叉。
func (c *ChatService) lockRepoForTurn(workspace string) func() {
	if strings.TrimSpace(workspace) == "" {
		return func() {}
	}
	repoRoot, err := c.gitRepoRoot(context.Background(), workspace)
	if err != nil {
		return func() {}
	}
	lock := c.repoLock(repoRoot)
	lock.RLock()
	return lock.RUnlock
}

func (c *ChatService) repoIsIdle(repoRoot string) bool {
	lock := c.repoLock(repoRoot)
	if !lock.TryLock() {
		return false
	}
	lock.Unlock()
	return true
}

func (c *ChatService) repoLock(path string) *sync.RWMutex {
	key := repoKey(path)
	c.repoLocksMu.Lock()
	defer c.repoLocksMu.Unlock()
	if c.repoLocks == nil {
		c.repoLocks = map[string]*sync.RWMutex{}
	}
	lock := c.repoLocks[key]
	if lock == nil {
		lock = &sync.RWMutex{}
		c.repoLocks[key] = lock
	}
	return lock
}

func repoKey(path string) string {
	key := filepath.Clean(strings.TrimSpace(path))
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

func gitCurrentBranch(ctx context.Context, repoRoot string) (branch, commit string, detached bool, err error) {
	if out, err := runGitRead(ctx, repoRoot, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
		if branch != "" {
			if head, headErr := runGitRead(ctx, repoRoot, "rev-parse", "--short", "HEAD"); headErr == nil {
				commit = strings.TrimSpace(head)
			}
			return branch, commit, false, nil
		}
	}
	head, headErr := runGitRead(ctx, repoRoot, "rev-parse", "--short", "HEAD")
	if headErr != nil {
		return "", "", false, headErr
	}
	return "", strings.TrimSpace(head), true, nil
}

func gitDirtyCount(ctx context.Context, repoRoot string) (int, error) {
	out, err := runGit(ctx, repoRoot, gitStatusTimeout, false, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(strings.TrimRight(out, "\r\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

func gitListBranches(ctx context.Context, repoRoot, current string) ([]GitBranchInfo, error) {
	out, err := runGitRead(ctx, repoRoot, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var branches []GitBranchInfo
	if current != "" {
		seen[current] = true
		branches = append(branches, GitBranchInfo{Name: current, Current: true})
	}
	for _, raw := range strings.Split(out, "\n") {
		name := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		branches = append(branches, GitBranchInfo{Name: name})
	}
	return branches, nil
}

func gitLocalBranchExists(ctx context.Context, repoRoot, branch string) bool {
	_, err := runGitRead(ctx, repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func validateBranchName(ctx context.Context, repoRoot, name string) error {
	if name == "" {
		return fmt.Errorf("分支名不能为空")
	}
	if strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\r\n\t") {
		return fmt.Errorf("分支名无效")
	}
	if _, err := runGitRead(ctx, repoRoot, "check-ref-format", "refs/heads/"+name); err != nil {
		return fmt.Errorf("分支名无效")
	}
	return nil
}

func runGitRead(ctx context.Context, dir string, args ...string) (string, error) {
	return runGit(ctx, dir, gitReadTimeout, false, args...)
}

func runGitWrite(ctx context.Context, dir string, args ...string) (string, error) {
	return runGit(ctx, dir, gitSwitchTimeout, true, args...)
}

func runGit(ctx context.Context, dir string, timeout time.Duration, write bool, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("未找到 Git: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fullArgs := []string{"-C", dir}
	if !write {
		fullArgs = []string{"--no-optional-locks", "-C", dir}
	}
	fullArgs = append(fullArgs, args...)
	cmd := exec.CommandContext(runCtx, "git", fullArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	cmd.SysProcAttr = gitProcessAttrs()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if runCtx.Err() != nil {
			return "", fmt.Errorf("Git 命令超时")
		}
		text := strings.TrimSpace(stderr.String())
		if text == "" {
			text = err.Error()
		}
		return "", errors.New(text)
	}
	return stdout.String(), nil
}

func gitBlockedReason(err error) string {
	if errors.Is(err, exec.ErrNotFound) {
		return "未找到 Git"
	}
	if strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
		return "项目目录不是 Git 仓库"
	}
	return "无法读取 Git 仓库"
}
