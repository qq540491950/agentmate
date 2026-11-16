package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGitTestRepo(t *testing.T, branch string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	root := t.TempDir()
	gitTestCommand(t, root, "init", "-b", branch)
	gitTestCommand(t, root, "config", "user.name", "AgentMate Test")
	gitTestCommand(t, root, "config", "user.email", "test@example.com")
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitTestCommand(t, root, "add", ".")
	gitTestCommand(t, root, "commit", "-m", "initial")
	return root
}

func gitTestCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=AgentMate Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=AgentMate Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestWorkspaceContextReportsProjectBranch(t *testing.T) {
	chat, _, _ := newProjectTestChat(t)
	root := initGitTestRepo(t, "main")
	project, err := chat.SaveProject("", "", root)
	if err != nil {
		t.Fatal(err)
	}

	info, err := chat.WorkspaceContext("", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.ProjectID != project.ID || info.ProjectName != project.Name {
		t.Fatalf("project info = %+v", info)
	}
	if info.GitBranch != "main" || info.GitDetached {
		t.Fatalf("git info = %+v, want branch main", info)
	}
	if !samePath(info.WorkspaceDir, root) {
		t.Fatalf("workspace = %q, want %q", info.WorkspaceDir, root)
	}
	sess, err := chat.NewSessionWithProject("", "", "分支上下文", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	activeInfo, err := chat.WorkspaceContext(sess.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if activeInfo.GitBranch != "main" || activeInfo.ProjectID != project.ID {
		t.Fatalf("active workspace context = %+v", activeInfo)
	}
}

func TestGitBranchesReportsCurrentAndDirtyCount(t *testing.T) {
	chat, _, _ := newProjectTestChat(t)
	root := initGitTestRepo(t, "main")
	gitTestCommand(t, root, "branch", "lirt")
	project, err := chat.SaveProject("", "", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("changed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := chat.GitBranches(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Current != "main" || info.Detached {
		t.Fatalf("current = %+v", info)
	}
	if !info.DirtyKnown || info.DirtyFiles != 3 {
		t.Fatalf("dirty = %d known=%v, want 3/true", info.DirtyFiles, info.DirtyKnown)
	}
	if !info.CanSwitch || info.BlockedReason != "" {
		t.Fatalf("canSwitch = %v reason=%q", info.CanSwitch, info.BlockedReason)
	}
	if len(info.Branches) != 2 || info.Branches[0].Name != "main" || !info.Branches[0].Current {
		t.Fatalf("branches = %+v", info.Branches)
	}
	if info.Branches[1].Name != "lirt" {
		t.Fatalf("branches = %+v", info.Branches)
	}
}

func TestSwitchGitBranchRequiresCleanWorkspace(t *testing.T) {
	chat, _, _ := newProjectTestChat(t)
	root := initGitTestRepo(t, "main")
	gitTestCommand(t, root, "branch", "lirt")
	project, err := chat.SaveProject("", "", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.SwitchGitBranch(project.ID, "lirt"); err == nil || !strings.Contains(err.Error(), "未提交") {
		t.Fatalf("dirty switch error = %v", err)
	}
	if got := gitTestCommand(t, root, "branch", "--show-current"); got != "main" {
		t.Fatalf("branch after rejected switch = %q", got)
	}

	gitTestCommand(t, root, "checkout", "--", "a.txt")
	info, err := chat.SwitchGitBranch(project.ID, "lirt")
	if err != nil {
		t.Fatal(err)
	}
	if info.GitBranch != "lirt" {
		t.Fatalf("switched branch = %+v", info)
	}
}

func TestSwitchGitBranchRejectsRunningRepo(t *testing.T) {
	chat, _, _ := newProjectTestChat(t)
	root := initGitTestRepo(t, "main")
	gitTestCommand(t, root, "branch", "lirt")
	project, err := chat.SaveProject("", "", root)
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, err := chat.gitRepoRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	lock := chat.repoLock(repoRoot)
	lock.RLock()
	defer lock.RUnlock()
	if _, err := chat.SwitchGitBranch(project.ID, "lirt"); err == nil || !strings.Contains(err.Error(), "正在运行") {
		t.Fatalf("running switch error = %v", err)
	}
}

func TestCreateGitBranchKeepsDirtyFiles(t *testing.T) {
	chat, _, _ := newProjectTestChat(t)
	root := initGitTestRepo(t, "main")
	project, err := chat.SaveProject("", "", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := chat.CreateGitBranch(project.ID, "feature/dirty")
	if err != nil {
		t.Fatal(err)
	}
	if info.GitBranch != "feature/dirty" {
		t.Fatalf("branch = %q, want feature/dirty", info.GitBranch)
	}
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil || string(data) != "dirty\n" {
		t.Fatalf("dirty file lost: %q, %v", data, err)
	}
	if _, err := chat.CreateGitBranch(project.ID, "bad..name"); err == nil || !strings.Contains(err.Error(), "分支名无效") {
		t.Fatalf("invalid branch error = %v", err)
	}
}

func TestGitBranchesDetachedAndNonRepo(t *testing.T) {
	chat, _, _ := newProjectTestChat(t)
	root := initGitTestRepo(t, "main")
	gitTestCommand(t, root, "checkout", "--detach")
	project, err := chat.SaveProject("", "", root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := chat.GitBranches(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Detached || info.Current != "" || info.HeadCommit == "" {
		t.Fatalf("detached info = %+v", info)
	}

	plain := t.TempDir()
	plainProject, err := chat.SaveProject("", "", plain)
	if err != nil {
		t.Fatal(err)
	}
	plainInfo, err := chat.GitBranches(plainProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plainInfo.CanSwitch || plainInfo.BlockedReason == "" {
		t.Fatalf("non-repo info = %+v", plainInfo)
	}
}

func TestGitBranchesSubdirectoryProjectCannotSwitch(t *testing.T) {
	chat, _, _ := newProjectTestChat(t)
	root := initGitTestRepo(t, "main")
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := chat.SaveProject("", "", sub)
	if err != nil {
		t.Fatal(err)
	}
	info, err := chat.GitBranches(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.CanSwitch || !strings.Contains(info.BlockedReason, "仓库根目录") {
		t.Fatalf("subdirectory project info = %+v", info)
	}
}
