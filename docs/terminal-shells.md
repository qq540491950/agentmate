# 终端 Shell 选择

`run_command` 使用设置中心「对话与上下文 → 默认命令执行器」中选择的 Shell。该设置只影响本地 `run_command`，不影响 MCP stdio 命令或远程 A2A Agent。

## 平台顺序

| 平台 | 自动顺序 | 最终兜底 |
|---|---|---|
| Windows | `pwsh` → Git Bash → Windows PowerShell 5.1 → `cmd` | `cmd.exe` |
| macOS | 登录 Shell → `zsh` → `bash` → `sh` → `pwsh` | `/bin/sh` |
| Linux | 登录 Shell → `bash` → `zsh` → `sh` → `pwsh` | `/bin/sh` |
| WSL 内部 | 登录 Shell → `bash` → `zsh` → `sh` | `/bin/sh` |

Windows 上 WSL 发行版单独列出，不进入普通自动回退链。应用本身运行在 WSL 内时按 Linux 处理。

## 可见性

- 只显示当前平台检测到的 Shell；非本平台选项不显示。
- `auto`、平台最终兜底和自定义路径始终可见。
- 显式选择的 Shell 不存在时返回错误，不静默回退；只有 `auto` 会回退。
- 标准模式不保存检测到的路径；只有自定义 Shell 保存绝对路径。

## 检测规则

- Windows 使用 `ComSpec`、`SystemRoot`、`ProgramFiles`、`LocalAppData`、PATH，并忽略不可靠的 `SHELL`。
- Git Bash 从 `git.exe` 推导 Git 安装目录，不使用 `System32\bash.exe`（WSL 启动器）。
- PowerShell 7 优先系统安装路径；Store 别名和 `.cache` 私有运行时仍可显式选择，但不参与 `auto`。
- WSL 发行版通过 `wsl.exe --list --quiet` 获取，输出按 UTF-16LE 解码。
- macOS/Linux 使用 `SHELL`、`/etc/shells`、PATH 和标准路径。

## 沙箱边界

- Windows AppContainer 下 Git Bash 无法可靠加载 MSYS DLL，因此标记为“需完全访问”；受限沙箱的 `auto` 会跳过它，完全访问时仍保持 Git Bash 在 Windows PowerShell 5.1 之前。
- WSL 运行在 Windows AppContainer 之外，需要完全访问权限；也不能强制 Windows 的断网策略。
- macOS/Linux 当前只有完全访问模式可执行终端；`workspace-write` / `read-only` 仍会返回“当前平台未实现 OS 沙箱”。
- 默认不加载用户启动配置。PowerShell 使用 `-NoProfile`，bash 使用 `--noprofile --norc`，zsh 使用 `-f`；可在高级设置中显式开启。

## 会话行为

当前运行中的会话保持首次使用的 Shell；修改全局设置不会热切换。新建会话和重启后恢复的会话读取当前全局设置。该快照只保存在内存中，不写入 `config.json`。
