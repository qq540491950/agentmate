package agentkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentmate/internal/sandbox"
)

// ReviewToolCall 为确认卡生成风险等级与文件 diff，不执行目标工具。
func ReviewToolCall(workspace, profile, network, tool string, args map[string]any) (risk, diff string) {
	risk = "low"
	switch tool {
	case "write_workspace_file", "edit_workspace_file":
		risk = "medium"
		path, _ := args["path"].(string)
		if strings.TrimSpace(path) == "" {
			return risk, ""
		}
		guard := NewPreviewGuard(workspace, profile, network)
		if ok, code, _ := guard.CheckWritePath(path); !ok && code != "protected_path" {
			risk = "high"
		}
		full, err := safeWorkspacePath(workspace, path)
		if err != nil {
			return risk, ""
		}
		oldText := ""
		if data, readErr := os.ReadFile(full); readErr == nil {
			oldText = string(data)
		}
		newText := oldText
		switch tool {
		case "write_workspace_file":
			newText, _ = args["content"].(string)
		case "edit_workspace_file":
			oldFragment, _ := args["oldText"].(string)
			replacement, _ := args["newText"].(string)
			replaceAll, _ := args["replaceAll"].(bool)
			if oldFragment == "" || !strings.Contains(oldText, oldFragment) {
				return risk, ""
			}
			newText = strings.Replace(oldText, oldFragment, replacement, 1)
			if replaceAll {
				newText = strings.ReplaceAll(oldText, oldFragment, replacement)
			}
		}
		rendered, _, _ := unifiedWorkspaceDiff(filepath.ToSlash(path), oldText, newText, 240, 48*1024)
		return risk, rendered
	case "run_command":
		command, _ := args["command"].(string)
		if risky, reason := sandbox.AssessCommand(command); risky {
			if strings.TrimSpace(reason) != "" {
				return "high", "命令风险：" + reason
			}
			return "high", "命令风险：可能产生外部副作用。"
		}
		if escalate, _ := args["escalate"].(bool); escalate {
			return "high", "该命令申请脱离沙箱执行。"
		}
		return "medium", ""
	case "verify_work":
		return "medium", ""
	case "delegate_task":
		return "medium", "委派成员可能调用工具；最终结果由协调员验收。"
	default:
		if strings.HasPrefix(tool, "mcp") || strings.Contains(tool, "__") {
			return "high", "外部 MCP 工具，副作用未知。"
		}
		return risk, ""
	}
}

// FormatPolicySnapshot 把暂停时的执行策略渲染为确认卡可展示的一行文本。
func FormatPolicySnapshot(workspace, approval, profile, network, model, reasoning string) string {
	parts := []string{
		"工作区=" + workspace,
		"审批=" + approval,
		"沙箱=" + profile,
		"网络=" + network,
	}
	if strings.TrimSpace(model) != "" {
		parts = append(parts, "模型="+model)
	}
	if strings.TrimSpace(reasoning) != "" {
		parts = append(parts, "推理="+reasoning)
	}
	return strings.Join(parts, " · ")
}

func confirmationPayload(tool string, args map[string]any, risk, diff, policy string) map[string]any {
	return map[string]any{
		"tool": tool, "risk": risk, "diff": diff, "policy": policy,
		"args": args, "summary": fmt.Sprintf("%s (%s)", tool, risk),
	}
}
