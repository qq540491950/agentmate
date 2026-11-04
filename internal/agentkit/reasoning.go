package agentkit

import (
	"encoding/json"
	"fmt"
	"strings"

	"go-workbuddy/internal/config"
)

// reasoningExtraBody turns one resolved model level into request-body fields.
//
// The level table stores a parameter name and a value:
//   - Param is empty: Value must be a JSON object whose members are merged at
//     the top level (models that need several fields at once, e.g. Claude's
//     adaptive thinking plus output_config.effort).
//   - Param has no dot: the value is set as that top-level field.
//   - Param is dotted ("thinking.budget_tokens"): nested objects are created.
//
// Values are parsed as JSON when possible, else sent as a plain string, so
// "high" becomes the string high while "4096" becomes a number. A malformed
// object in an empty-Param level is a configuration error the run should
// surface rather than silently sending the model default.
func reasoningExtraBody(mi config.ModelInfo, level string) (map[string]any, error) {
	if mi.Reasoning == nil {
		return nil, nil
	}
	param, value, ok := mi.Reasoning.Resolve(level)
	if !ok {
		return nil, nil
	}
	label := strings.TrimSpace(level)
	if label == "" {
		label = strings.TrimSpace(mi.Reasoning.DefaultLevel)
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

// setReasoningPath writes value under a dotted path, creating intermediate
// objects. A conflict with a non-object intermediate is overwritten by an
// object so a typo cannot panic the run.
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

// marshalWithExtra marshals v with its alias type, then merges extra fields at
// the top level. Extra wins on conflicts: the level table is the explicit
// user choice, and reserved fields such as model/messages are not offered by
// the preset tables.
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
