# 自更新用 Wails 原生 updater + 「安装包 / 自更新资产」双轨发布

应用内检查与安装更新建立在 Wails v3 自带的 `pkg/updater`（`app.Updater`）之上，更新通道只有 GitHub Releases 一个：签名后的更新清单（`manifest.json`）作为发布资产，客户端通过 `releases/latest/download/manifest.json` 读取。发布同时产出两套产物——给人安装的 `.exe` / `.dmg` / `.deb`，和给应用自己替换用的 `.zip`（Windows/macOS）与 `.AppImage`（Linux）；清单里只列后者。

选择原生 updater 而不是自研或第三方库（`go-selfupdate` 等），是因为它已经处理了这些库都不处理的事情：把运行中的 `.app` 当目录整体替换、按 `$APPIMAGE` 定位真正的可执行文件、经独立 helper 进程在父进程退出后换文件、流式摘要与 ed25519 签名校验、以及原子写。自研要重新踩一遍这些坑，第三方库则只替换单个文件，在 macOS bundle 与 deb 安装上根本不成立。

## 考虑过的方案

- **只用安装包，自动更新时运行安装器**：macOS 上 `.dmg`/`.pkg` 与 Windows 上 `.msi` 明确不被 updater 支持；NSIS 安装器会被资产匹配器主动跳过，误选即毁坏安装。
- **GitHub provider + 校验和 sidecar**：github provider 只能提供摘要，无法携带签名；而一旦钉了公钥，纯摘要清单会被 fail-closed 拒绝。因此签名这条需求把通道推向「GitHub 上托管清单」的 endpoint provider。
- **系统包管理器更新（deb/rpm）**：需要 root，updater 没有提权路径，因此这两类安装形态只降到「提示 + 跳转下载」。

## 后果

- Windows 不发安装程序，只发一个免安装 exe：它既是用户下载的东西，也是自更新替换的目标，因此没有「装到 `%ProgramFiles%` 就永远无法自更新且没有 UAC 兜底」这类分叉。代价是没有卸载项、开始菜单快捷方式与文件关联，且运行时所在目录必须可写。
- Linux 的 `.deb` 安装版只能提示下载；`.AppImage` 是唯一天然可自更新的形态。
- 三平台产物必须同一次发布原子出齐，否则客户端检查会以「该平台无可用资产」硬失败。
- 更新清单与安装包对「未签名」的容忍度不同：清单可以只靠摘要（未钉公钥时），但 macOS 替换后的 `.app` 仍受 Gatekeeper 约束，未公证时可能被拦。
- Windows 载荷不压缩：更新会直接下载未压缩的 exe（约 30 MB），换成 zip 能省一半带宽，但会让「下载即用」这一主路径变成「先解压」。
