package agentkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"agentmate/internal/sandbox"
	"agentmate/internal/terminal"
)

type verifyArgs struct {
	Commands       []string `json:"commands,omitempty" jsonschema:"optional commands to run; omit to auto-detect project checks"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"per-command timeout (default 600, max 600)"`
}

type verifyCommandResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exitCode"`
	Output   string `json:"output"`
	TimedOut bool   `json:"timedOut,omitempty"`
}

type verifyResult struct {
	OK       bool                  `json:"ok"`
	Detected bool                  `json:"detected"`
	Results  []verifyCommandResult `json:"results"`
}

// VerifyTool 按项目类型运行测试/类型检查/构建，并将结果作为结构化证据返回。
// 所有命令仍经过会话沙箱和命令预检；它不是绕过 run_command 的旁路。
func VerifyTool(workspace string, policy sandbox.Policy, spec terminal.Spec, requireConfirmation bool) (tool.Tool, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("任务工作区为空，无法初始化验证工具")
	}
	return functiontool.New(functiontool.Config{
		Name: "verify_work",
		Description: "Run the project's detected checks (tests/typecheck/build) inside the task workspace and return structured evidence. " +
			"Use it before marking implementation steps completed. Commands are capped at 600s each and run through the session sandbox.",
		RequireConfirmation: requireConfirmation,
	}, func(ctx agent.Context, args verifyArgs) (verifyResult, error) {
		commands := cleanVerifyCommands(args.Commands)
		detected := false
		if len(commands) == 0 {
			commands = detectVerifyCommands(workspace)
			detected = true
		}
		if len(commands) == 0 {
			return verifyResult{OK: false, Detected: true}, fmt.Errorf("没有检测到可运行的验证命令；请显式传入 commands")
		}
		if len(commands) > 4 {
			return verifyResult{}, fmt.Errorf("一次最多运行 4 个验证命令")
		}
		timeout := 600 * time.Second
		if args.TimeoutSeconds > 0 && time.Duration(args.TimeoutSeconds)*time.Second < timeout {
			timeout = time.Duration(args.TimeoutSeconds) * time.Second
		}
		unlock := lockWorkspaceTool(workspace)
		defer unlock()
		out := verifyResult{OK: true, Detected: detected, Results: make([]verifyCommandResult, 0, len(commands))}
		for _, command := range commands {
			res, err := runShellCommandWithPolicySpec(ctx, workspace, command, timeout, maxShellOutput, policy, spec)
			if err != nil {
				out.OK = false
				out.Results = append(out.Results, verifyCommandResult{Command: command, ExitCode: -1, Output: err.Error()})
				continue
			}
			if res.ExitCode != 0 || res.TimedOut {
				out.OK = false
			}
			out.Results = append(out.Results, verifyCommandResult{
				Command: command, ExitCode: res.ExitCode, Output: res.Output, TimedOut: res.TimedOut,
			})
		}
		return out, nil
	})
}

func cleanVerifyCommands(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, command := range in {
		command = strings.TrimSpace(command)
		if command == "" || seen[command] {
			continue
		}
		seen[command] = true
		out = append(out, command)
	}
	return out
}

func detectVerifyCommands(workspace string) []string {
	var commands []string
	if fileExists(filepath.Join(workspace, "go.mod")) {
		commands = append(commands, "go test ./...")
	}
	if fileExists(filepath.Join(workspace, "Cargo.toml")) {
		commands = append(commands, "cargo test")
	}
	if fileExists(filepath.Join(workspace, "package.json")) {
		if data, err := os.ReadFile(filepath.Join(workspace, "package.json")); err == nil {
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			_ = json.Unmarshal(data, &pkg)
			if pkg.Scripts["test"] != "" {
				commands = append(commands, "npm test")
			}
			if pkg.Scripts["build"] != "" {
				commands = append(commands, "npm run build")
			}
		}
		if fileExists(filepath.Join(workspace, "tsconfig.json")) {
			commands = append(commands, "npx tsc --noEmit")
		}
	}
	if fileExists(filepath.Join(workspace, "pyproject.toml")) || fileExists(filepath.Join(workspace, "pytest.ini")) {
		commands = append(commands, "python -m pytest")
	}
	if len(commands) > 4 {
		commands = commands[:4]
	}
	return commands
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
