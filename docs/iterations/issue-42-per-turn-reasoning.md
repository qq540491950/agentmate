### 第 ? 轮迭代：按轮思考强度与推理模型入口兜底（已实施）

依据：issue #42（Windows x64 桌面，v0.5.21，自定义 / OpenAI 兼容供应商，模型
`codearts/glm-5.3-flash`）。#34 只提供了会话级思考强度 chip，同一会话内无法逐条消息
切换；且入口只在模型显式配置了推理档位表时出现，用户当前模型的档位表为空，输入区
完全没有思考强度入口，被误判为「系统没有这个能力」。

实现要点：

- **按轮覆盖**：新增 `ChatService.SendWithReasoning(sessionID, text, attachments,
  reasoningLevel)`；`Send` 保持原签名并等价于空档位（空档位 = 跟随会话默认，再落到
  模型默认档）。前端输入区 chip 改为「本条消息思考强度」：单选档位只作用于下一条
  消息，发送后自动归零；正在运行的轮次不受影响（档位在发送时解析并构建模型）。
- **优先级**：`runTurn` 统一解析 `单轮显式选择 > 会话默认 > 模型配置默认档`，未知/
  过期档位（切模型后残留）自动落回下一级，不会发送任意字段。会话默认仍由
  `SetSessionReasoningLevel` 持久化，chip 菜单新增「设为会话默认 / 清除会话默认」，
  #34 的能力保留。
- **推理家族兜底**：`config.ModelInfo.ReasoningTable()` 在模型未保存档位表时返回内置
  家族映射（deepseek / kimi / glm / claude / gpt / qwen 等），入口不再隐藏；显式
  `enabled=false` 仍然视为关闭。兜底表只服务于 UI 与显式选择：没有用户选择时不注入
  任何请求字段（`ReasoningConfigured()` 区分「已保存」与「内置兜底」），升级不会悄悄
  改写老模型的请求体。
- **真实下发**：档位仍走 `reasoningExtraBody` 既有链路（OpenAI 兼容
  `reasoning_effort` / `thinking`，Anthropic 自适应 `thinking` + `output_config`，
  Qwen 多字段），团队根节点、直聊、Agent 三条构建路径共用解析结果。
- **历史可还原**：实际生效档位写入用户消息事件的 `Actions.StateDelta`
  （`workbuddy.reasoning_level`，非 `temp:` 前缀所以会入库），`renderEvent` 回填
  `ChatMessage.reasoning`；用户气泡下方显示「思考强度：高」，Markdown / HTML 导出在
  用户标题后注明档位。`Regenerate` / `EditAndResend` / HITL 确认续跑都从事件里读回
  原档位，保持一致。
- **排队消息**：排队项改为 `{text, reasoning}`，排队的下一条消息使用入队时选定的档位，
  不会在等待期间被后续切换污染。
- **绑定与桩**：重新生成 Wails 绑定（新增 `SendWithReasoning`、`ChatMessage.reasoning`、
  `ChatStreamEvent.reasoning`）；浏览器桩补齐 `SendWithReasoning` 方法、流事件回显档位，
  并让 `SuggestModels` 按家族返回兜底档位表。

测试证据：

- `internal/config`：新增兜底表 / 显式关闭 / `MatchLevel` / `DefaultLevelName` 用例。
- `internal/agentkit`：新增「兜底表未选档位不注入、显式选档位才注入」用例。
- `internal/services`：新增按轮档位端到端用例（请求体切换、会话默认不被写回、流事件与
  历史消息带档位、未知档回退、Regenerate 延续档位）与解析优先级单测。
- 质量门禁：`./scripts/verify.sh` 全绿（前端构建、vitest 63 项、`go vet`、
  `go test ./internal/... .`、`go test -race ./internal/... .`、`wails3 build`）。
- 本机未做浏览器桩实机点击验证（定时任务环境无法启动预览服务）；前端逻辑由
  `tsc --noEmit`、vitest 纯函数用例与构建门禁覆盖。

已知边界：远程 A2A 会话没有本地模型，不显示该控件；Gemini 协议仍不合并任意请求字段
（与 #34 一致）；历史消息的档位徽标只对本次改动之后记录的轮次出现。