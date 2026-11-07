# UI 截图基线

浏览器 stub（`npm run stub`，:9246/stub/index.html）在 1280×800 视口下的参考截图，用于走查对比。
这些是**人工对比用的参考图**，不是像素级 diff 基线；改动 UI 后建议重走一遍并对照。

| 文件 | 内容 |
|---|---|
| `chat-dark-1280.jpg` | 对话页（深色）：执行组、确认卡、代码高亮、composer |
| `chat-light-1280.jpg` | 对话页（浅色）：同场景 |
| `settings-dark-1280.jpg` | 设置·通用（深色）：双列网格 + SettingRow |
| `agents-dark-1280.jpg` | 设置·Agents（深色）：PageHeader + 行式列表 |

## 重新生成

1. `npm run stub`（frontend/），浏览器打开 `http://localhost:9246/stub/index.html`，视口 1280×800。
2. 会话种子含 HITL 确认卡；如需对话流示例，可向当前会话注入一条 `chat:stream` 的 `message` 事件。
3. 按上表逐页截图，替换同名文件；深/浅切换用侧栏底部的主题按钮。

## 走查清单（每轮 UI 改动后）

- [ ] 深浅两套主题下分别查看对话页与设置页；
- [ ] 正文 ≥ 14px、元数据 ≥ 12px，字号设置三档均生效；
- [ ] 消息内嵌套容器 ≤ 2 层，无「框套框」；
- [ ] 全屏只有一个品牌色焦点（当前会话行 / 发送按钮）；
- [ ] 图标按钮有 `aria-label`，键盘 Tab 有可见焦点环；
- [ ] `prefers-reduced-motion` 下无位移动画；
- [ ] `npx tsc --noEmit && npm test && npm run build` 全绿。