package sandbox

import (
	"regexp"
	"strings"
)

// commandRisk describes one heuristic match. The OS boundary remains the hard
// control; this only decides when auto-approve should pause for the user.
type commandRisk struct {
	pattern *regexp.Regexp
	reason  string
}

var commandRisks = []commandRisk{
	{regexp.MustCompile(`(?i)\b(rm|del|erase|rmdir|rd)\b`), "会删除文件或目录"},
	{regexp.MustCompile(`(?i)\b(format\s+[a-z]:|diskpart\b|mkfs\b|dd\s+if=|bcdedit\b)`), "会修改磁盘或启动配置"},
	{regexp.MustCompile(`(?i)\b(shutdown|reboot|taskkill|stop-process)\b`), "会关机或结束进程"},
	{regexp.MustCompile(`(?i)\b(reg\s+delete|reg\s+add|setx|netsh|sc\s+delete|schtasks|new-service|set-executionpolicy)\b`), "会修改系统配置或计划任务"},
	{regexp.MustCompile(`(?i)\b(takeown|icacls|chmod\s+777|chown\s+-r)\b`), "会修改文件权限或所有权"},
	{regexp.MustCompile(`(?i)\bgit\s+(reset\s+--hard|clean\s+-[a-z]*f)`), "会强制覆盖或清理 Git 工作区"},
	{regexp.MustCompile(`(?i)\b(powershell|pwsh)\b.*(-enc|-encodedcommand|invoke-expression|\biex\b)`), "会执行编码或动态生成的脚本"},
	{regexp.MustCompile(`(?i)\b(curl|wget|iwr|invoke-webrequest|irm|invoke-restmethod)\b.*\|`), "会把下载内容直接交给解释器执行"},
	{regexp.MustCompile(`(?i)\b(certutil|bitsadmin)\b`), "会通过系统工具下载或传输文件"},
	{regexp.MustCompile(`(?i)\b(git\s+push|npm\s+publish|pnpm\s+publish|yarn\s+publish|cargo\s+publish|docker\s+push|gh\s+release|kubectl\s+(apply|delete)|terraform\s+(apply|destroy))\b`), "会推送代码、发布制品或变更远端环境"},
	{regexp.MustCompile(`(?i)(\.ssh|id_rsa|\.aws[\\/]credentials|\.kube[\\/]config|\.gnupg)`), "会访问凭据或密钥路径"},
}

// AssessCommand reports whether a shell command looks risky enough to require
// confirmation in auto-approve mode. It intentionally errs on the side of
// asking: false negatives are mitigated by the OS sandbox, while false
// positives only cost one approval.
func AssessCommand(command string) (bool, string) {
	normalized := strings.ToLower(strings.Join(strings.Fields(command), " "))
	if normalized == "" {
		return false, ""
	}
	for _, risk := range commandRisks {
		if risk.pattern.MatchString(normalized) {
			return true, risk.reason
		}
	}
	return false, ""
}
