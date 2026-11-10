# UI 截图基线

> 2026-11-10 起以 Nord 表面契约截图为主。旧 JPG 只保留为迁移前历史，不再作为实现对照。

浏览器 stub（`npm run stub`，`:9246/stub/index.html`）在 **1280×800** 视口下的参考截图，用于人工走查。不是像素级 diff 基线。

## Nord 表面契约基线

| 文件 | 内容 |
|---|---|
| `nord-dark-chat-1280.jpg` | 对话页 / Nord 深色 |
| `nord-light-chat-1280.jpg` | 对话页 / Nord 浅色 |
| `settings-nord-dark-1280.jpg` | 设置·通用 / Nord 深色（主题画廊） |
| `settings-nord-light-1280.jpg` | 设置·通用 / Nord 浅色（主题画廊） |
| `agents-nord-dark-1280.jpg` | 设置·Agents / Nord 深色（行式列表） |
| `paper-light-chat-1280.jpg` | 对话页 / 纸墨浅色 |
| `contrast-dark-chat-1280.jpg` | 对话页 / 高对比深色 |

旧 `chat-dark-1280.jpg`、`chat-light-1280.jpg`、`settings-dark-1280.jpg`、`agents-dark-1280.jpg` 是 Nord 实施前的历史截图。

## 重新生成

1. `cd frontend && npm run stub`。
2. 固定视口 `1280×800`，打开 `http://127.0.0.1:9246/stub/index.html?theme=nord&mode=dark`。
3. `theme` 支持 `nord | graphite | paper | contrast`，`mode` 支持 `light | dark | system`；设置页点击侧栏齿轮进入，Agents 页再从设置左栏进入。
4. 截图时保持 100% 缩放，不使用浏览器缩放或移动端视口。
5. 新增/替换截图后同步本表；旧截图不要覆盖，除非明确在做历史清理。

## 走查清单

- [ ] 布局 chrome（顶栏、侧栏、会话头、设置导航）没有常驻 border/ring；
- [ ] 卡片、设置行、管理列表行、工具组、对话框、popover 没有布局级描边；
- [ ] 选中态是 `accent-soft` 底纹 + `accent-text`，不是左侧竖条；
- [ ] `canvas` / `chrome` / `panel` 相邻表面在 100% 亮度下可感知但无明显色块跳变；
- [ ] 正文 ≥ 14px、元数据 ≥ 12px，字号设置三档均生效；
- [ ] 确认/错误/警告/成功同时有图标或文字，不只靠颜色；
- [ ] 键盘 Tab 有可见 focus ring，主题画廊可用方向键切换；
- [ ] `prefers-reduced-motion` 下无长位移动画；
- [ ] `npx tsc --noEmit && npm test && npm run build` 全绿。
