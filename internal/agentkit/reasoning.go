package agentkit

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentmate/internal/config"
)

// reasoningExtraBody 将一个已解析的模型级别转换为请求体字段。
//
// 级别表存储参数名和值： - Param 为空：Value 必须是一个 JSON
// 对象，其成员会合并到顶层（对于需要一次设置多个字段的模型，例如 Claude
// 的自适应思考加上 output_config.effort）。 - Param
// 不含点：该值会被设置为对应的顶层字段。 - Param
// 含点（"thinking.budget_tokens"）：会创建嵌套对象。
//
// 值在可能时按 JSON 解析，否则作为普通字符串发送，因此 "high" 会成为字符串
// high，而 "4096" 会成为数字。空 Param
// 级别中格式错误的对象属于配置错误，运行应将其暴露出来，而不是静默发送模型默认
// 值。
//
// 内置回退表（用户从未配置过的已知推理系列）仅在显式选择了某个级别时才生效：它
// 的存在是为了让选择器绝不会在早于级别表保存的模型上隐藏，而不是在用户不知情的
// 情况下改写供应商默认值。
func reasoningExtraBody(mi config.ModelInfo, level string) (map[string]any, error) {
	table := mi.ReasoningTable()
	if table == nil {
		return nil, nil
	}
	level = strings.TrimSpace(level)
	if level == "" && !mi.ReasoningConfigured() {
		return nil, nil
	}
	param, value, ok := table.Resolve(level)
	if !ok {
		return nil, nil
	}
	label := level
	if label == "" {
		label = table.DefaultLevelName()
	}
	if label == "" {
		label = "默认档"
	}
	if strings.TrimSpace(param) == "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(value), &obj); err != nil || obj == nil {
			return nil, fmt.Errorf("模型「%s」的思考档位「%s」参数值必须是 JSON 对象: %s", mi.Label(), label, value)
		}
		return obj, nil
	}
	var v any
	if err := json.Unmarshal([]byte(value), &v); err != nil {
		v = value
	}
	out := map[string]any{}
	setReasoningPath(out, param, v)
	return out, nil
}

// setReasoningPath 在点分路径下写入
// value，并创建中间对象。与非对象中间值发生冲突时，会用一个对象将其覆盖，因此拼
// 写错误不会使运行发生 panic。
func setReasoningPath(dst map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	cur := dst
	for i, key := range parts {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		if i == len(parts)-1 {
			cur[key] = value
			return
		}
		next, ok := cur[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[key] = next
		}
		cur = next
	}
}

// marshalWithExtra 使用 v 的别名类型对 v 进行
// marshal，然后在顶层合并额外字段。发生冲突时 Extra
// 优先：级别表是用户的显式选择，而 model/messages 等预留字段不由预设表提供。
func marshalWithExtra(v any, extra map[string]any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil || len(extra) == 0 {
		return raw, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	for k, val := range extra {
		body[k] = val
	}
	return json.Marshal(body)
}
