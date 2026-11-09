# 侧栏更新入口与项目树设计（已实施）

## 目标

- 用户不需要进入「设置 → 关于」检查更新。
- 侧栏底部在发现新版本时出现一枚紧凑入口；无更新时完全隐藏，保持原有版本号与图标位置。
- 点击入口查看版本说明并开始更新；下载过程展示实时进度。
- 下载、校验、安装准备完成后，明确询问是否立即重启，绝不自动重启。

## 状态机

| 状态 | 侧栏底部 | 浮层 / 对话框 |
| --- | --- | --- |
| 无更新 / 未检查 | 保持 `AgentMate v{当前版本}`，不增加入口 | 无 |
| 发现新版本 | `● ↑ 更新` 胶囊，紧邻版本号 | 版本对比、更新说明、下载并更新 |
| 下载中 | 胶囊变为环形进度 + `42%`，底部出现 2px 进度线 | 进度、大小、速度、预计时间、可继续使用提示 |
| 校验 / 安装准备 | 胶囊显示 `正在准备`，进度条进入不定态 | 阶段说明；不阻塞主界面 |
| 已就绪 | 胶囊变为 `✓ 重启`（tooltip 为“更新已准备就绪”） | 居中确认框：稍后重启 / 立即重启 |
| 失败 | 胶囊变为暖色提示，保留入口 | 错误原因、重试、打开发布页 |

## 视觉约束

- 沿用现有深色 token：`--sidebar` / `--popover` / `--border` / `--brand`，不引入新的品牌色。
- 胶囊高度 21px，默认宽度约 46px，确保 240px 默认侧栏下仍能放下版本号和三个图标。
- 浮层宽 296px，锚定在侧栏底部之上；空间不足时优先向上展开，不覆盖输入区。
- 版本号使用 Geist；说明文字 9.5–10px，不抢对话正文层级。
- 主操作按钮使用中性浅色按钮，品牌蓝只用于更新状态与进度，保持现有视觉层级。

## 实施说明

- 已接入现有 `UpdateService`：复用 `Status`、`InstallUpdate`、`ApplyUpdate`、`Skip`、`OpenDownloadPage` 与 updater 事件，无需新增 Go 接口。
- 实际侧栏在 240px 默认宽度下使用更短的胶囊文案（`更新` / `42%` / `校验中` / `重启`），完整说明放在 tooltip 与浮层中。
- 更新失败只在已有可用版本时保留入口，避免后台自动检查失败打扰用户。
- 重启确认框保留“稍后重启”；选择后入口仍在，可随时重新打开确认框。
- 项目行整行可点击折叠：展开用打开文件夹图标，收起用闭合文件夹图标；活动会话切换时自动展开所属项目，但支持用户再次手动收起。
- 视觉走查截图见 [implementation-sidebar.png](implementation-sidebar.png)、[implementation-project-collapse.png](implementation-project-collapse.png)、[implementation-update-available.png](implementation-update-available.png)、[implementation-update-progress.png](implementation-update-progress.png)、[implementation-update-ready.png](implementation-update-ready.png)。
