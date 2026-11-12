package services

import (
	"testing"

	"agentmate/internal/config"
)

func TestSessionTerminalFreezesInitialSetting(t *testing.T) {
	chat := &ChatService{}
	first := chat.terminalSettingsFor("s1", config.TerminalSettings{Mode: config.TerminalModeCmd})
	if first.Mode != config.TerminalModeCmd {
		t.Fatalf("first shell = %q, want cmd", first.Mode)
	}
	second := chat.terminalSettingsFor("s1", config.TerminalSettings{Mode: config.TerminalModePwsh})
	if second.Mode != config.TerminalModeCmd {
		t.Fatalf("active session changed shell to %q, want cmd", second.Mode)
	}
	other := chat.terminalSettingsFor("s2", config.TerminalSettings{Mode: config.TerminalModePwsh})
	if other.Mode != config.TerminalModePwsh {
		t.Fatalf("new session shell = %q, want pwsh", other.Mode)
	}
	chat.forgetSessionTerminal("s1")
	restored := chat.terminalSettingsFor("s1", config.TerminalSettings{Mode: config.TerminalModeGitBash})
	if restored.Mode != config.TerminalModeGitBash {
		t.Fatalf("forgotten session shell = %q, want git-bash", restored.Mode)
	}
}
