package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/agentkit"
)

// RememberSession 将整段对话摄取到长期记忆中，以便其他会话（使用记忆工具时）之后可以回忆它。
func (c *ChatService) RememberSession(sessionID string) (int, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return 0, err
	}
	if resp.Session == nil {
		return 0, fmt.Errorf("会话 %q 不存在", sessionID)
	}
	if c.S.Memory == nil {
		return 0, fmt.Errorf("记忆服务不可用")
	}
	if err := c.S.Memory.AddSessionToMemory(context.Background(), resp.Session); err != nil {
		return 0, err
	}
	n, err := c.S.Memory.Count(c.appName, c.userID)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// SessionStats 聚合单个会话的可观测性指标。
type SessionStats struct {
	Messages       int   `json:"messages"`
	ToolCalls      int   `json:"toolCalls"`
	ToolFailures   int   `json:"toolFailures"`
	AssistantTurns int   `json:"assistantTurns"`
	TokensIn       int64 `json:"tokensIn"`
	TokensOut      int64 `json:"tokensOut"`
	TokensTotal    int64 `json:"tokensTotal"`
	// ContextTokens 是当前（CURRENT）的上下文占用（最后一次模型调用的提示词大小），而不是累计的
	// TokensTotal —— UI
	// 的上下文窗口仪表必须使用此值，否则长时间工具循环的会话会报告 "context
	// exhausted"，而真实占用只是窗口的一小部分。
	ContextTokens int64 `json:"contextTokens,omitempty"`
	// CachedTokens 是最后一次模型调用中 ContextTokens
	// 的缓存部分（当供应商不报告提示词缓存时为 0）。
	CachedTokens int64        `json:"cachedTokens,omitempty"`
	Daily        []StatDay    `json:"daily,omitempty"`   // 按天的 token/费用明细
	Authors      []AuthorStat `json:"authors,omitempty"` // 按参与者拆分的明细（团队时间线）
	FirstAt      *time.Time   `json:"firstAt,omitempty"`
	LastAt       *time.Time   `json:"lastAt,omitempty"`
}

// AuthorStat 按会话参与者（团队成员、远程 Agent）拆分用量；"user" 不包含在内。
type AuthorStat struct {
	Author    string `json:"author"`
	Turns     int    `json:"turns"`
	ToolCalls int    `json:"toolCalls"`
	Tokens    int64  `json:"tokens"`
}

// StatDay 是某个日历日的用量，并附带按会话目标的当前模型价格（¥/1M
// tokens）估算的费用。
type StatDay struct {
	Date             string  `json:"date"` // YYYY-MM-DD（本地时间）
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	EstimatedCost    float64 `json:"estimatedCost"`
}

// SessionStats 根据已存储的 ADK 事件计算聚合指标。
func (c *ChatService) SessionStats(sessionID string) (*SessionStats, error) {
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return nil, fmt.Errorf("会话 %q 不存在", sessionID)
	}
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	st := &SessionStats{}
	if resp.Session == nil {
		return st, nil
	}
	// 模型价格（¥/1M
	// tokens）来自会话目标当前使用的模型；使用其他模型的历史回合按近似值计算。
	priceIn, priceOut := 0.0, 0.0
	cfgNow := c.S.Store.Get()
	priceAc := c.targetAgentConfig(cfgNow, target)
	if priceAc == nil && target.TargetType == "" {
		if d, derr := directAgentConfig(cfgNow); derr == nil {
			priceAc = &d
		}
	}
	if ac := priceAc; ac != nil {
		// 定价遵循会话实际运行的模型（与 runTurn
		// 相同的限制逻辑：模型已被删除的覆盖项会回退到 Agent
		// 配置的模型；模型为空时会继承全局默认值）。
		providerID, resolvedModel := cfgNow.ResolveAgentModel(*ac)
		effectiveModel := agentkit.EffectiveModel(cfgNow, providerID, resolvedModel, target.ModelOverride)
		if p := cfgNow.FindProvider(providerID); p != nil {
			for _, mi := range p.Models {
				if mi.ID == effectiveModel {
					priceIn, priceOut = mi.PriceIn, mi.PriceOut
				}
			}
		}
	}
	days := map[string]*StatDay{}
	authors := map[string]*AuthorStat{}

	for ev := range resp.Session.Events().All() {
		if ev == nil {
			continue
		}
		first, last := st.FirstAt, st.LastAt
		if first == nil || ev.Timestamp.Before(*first) {
			ts := ev.Timestamp
			st.FirstAt = &ts
		}
		if last == nil || ev.Timestamp.After(*last) {
			ts := ev.Timestamp
			st.LastAt = &ts
		}
		if ev.Author == "user" {
			st.Messages++
			continue
		}
		if ev.Content == nil {
			continue
		}
		if u := usageOf(ev); u != nil {
			st.TokensIn += u.PromptTokens
			st.TokensOut += u.CompletionTokens
			st.TokensTotal += u.TotalTokens
			// 事件按顺序到达；最后一次用量即实时上下文大小。
			st.ContextTokens = u.ContextTokens
			st.CachedTokens = u.CachedTokens

			as := authors[ev.Author]
			if as == nil {
				as = &AuthorStat{Author: ev.Author}
				authors[ev.Author] = as
			}
			as.Tokens += u.TotalTokens

			day := ev.Timestamp.Local().Format("2006-01-02")
			d := days[day]
			if d == nil {
				d = &StatDay{Date: day}
				days[day] = d
			}
			d.PromptTokens += u.PromptTokens
			d.CompletionTokens += u.CompletionTokens
			d.TotalTokens += u.TotalTokens
			d.EstimatedCost += float64(u.PromptTokens)/1e6*priceIn + float64(u.CompletionTokens)/1e6*priceOut
		}
		if ev.Partial {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionCall != nil:
				st.ToolCalls++
				if as := authors[ev.Author]; as != nil {
					as.ToolCalls++
				}
			case p.FunctionResponse != nil:
				if strings.Contains(string(mustJSON(p.FunctionResponse.Response)), `"error"`) {
					st.ToolFailures++
				}
			}
		}
		if ev.TurnComplete && !ev.Partial {
			st.AssistantTurns++
			if as := authors[ev.Author]; as != nil {
				as.Turns++
			}
		}
	}
	for name, as := range authors {
		as.Author = name
		st.Authors = append(st.Authors, *as)
	}
	sort.Slice(st.Authors, func(i, j int) bool { return st.Authors[i].Author < st.Authors[j].Author })
	for _, d := range days {
		st.Daily = append(st.Daily, *d)
	}
	sort.Slice(st.Daily, func(i, j int) bool { return st.Daily[i].Date < st.Daily[j].Date })
	return st, nil
}
