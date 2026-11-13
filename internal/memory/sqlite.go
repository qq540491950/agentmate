// Package memory 实现了持久化的 ADK memory.Service，其后端是与会话存储相同的
// SQLite 数据库，为 Agent 提供在重启后仍保留且跨越对话的长期记忆。
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// memoryEntry 是 memory.Entry 的持久化形式。
type memoryEntry struct {
	ID        string `gorm:"primaryKey"`
	AppName   string `gorm:"index"`
	UserID    string `gorm:"index"`
	Author    string
	Content   string // JSON 编码的 genai.Content
	Timestamp time.Time
}

// TableName 用于 gorm。
func (memoryEntry) TableName() string { return "memories" }

// SQLiteService 是持久化的 memory.Service。
type SQLiteService struct {
	db *gorm.DB
}

// NewSQLiteService 迁移 memories 表并返回该服务。
func NewSQLiteService(db *gorm.DB) (*SQLiteService, error) {
	if err := db.AutoMigrate(&memoryEntry{}); err != nil {
		return nil, err
	}
	return &SQLiteService{db: db}, nil
}

// AddSessionToMemory 将会话中有意义的事件摄取到长期记忆中。重复添加同一会话时，按事件 ID
// 保持幂等。
func (s *SQLiteService) AddSessionToMemory(_ context.Context, sess session.Session) error {
	if sess == nil {
		return fmt.Errorf("memory: nil session")
	}
	for ev := range sess.Events().All() {
		if ev == nil || ev.Content == nil || ev.Partial {
			continue
		}
		var textParts []string
		var calls []string
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.Text != "" && !p.Thought:
				textParts = append(textParts, p.Text)
			case p.FunctionCall != nil:
				args, _ := json.Marshal(p.FunctionCall.Args)
				calls = append(calls, p.FunctionCall.Name+"("+string(args)+")")
			}
		}
		// 条目会持久化完整的结构化内容；提取出的文本仅用于下方的空内容检查。
		if strings.TrimSpace(strings.Join(textParts, "\n")) == "" && len(calls) == 0 {
			continue
		}
		entry := memoryEntry{
			ID:        ev.ID,
			AppName:   sess.AppName(),
			UserID:    sess.UserID(),
			Author:    ev.Author,
			Content:   string(mustJSON(ev.Content)),
			Timestamp: ev.Timestamp,
		}
		// 幂等插入：事件 ID 在每个会话内唯一且稳定。 使用 OnConflict
		// DoNothing，而不是先 count 再 create —— 同一会话的两个并行
		// AddSessionToMemory 调用过去会竞态地越过 count，其中一个在 UNIQUE
		// 约束上失败，静默地 中止会话中途的 AutoRemember。
		if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry).Error; err != nil {
			return err
		}
	}
	return nil
}

// SearchMemory 查找文本匹配查询关键字的条目，按命中次数（相关性）排序，命中次数相同时最新的
// 在前。候选集在 LIMIT 截断之前先按新旧程度预排序：没有确定性排序时 SQLite
// 按扫描顺序返回行，因此一旦存储量超出候选窗口，最相关的旧条目就可能静默地掉出
// 考虑范围。
func (s *SQLiteService) SearchMemory(_ context.Context, req *memory.SearchRequest) (*memory.SearchResponse, error) {
	var rows []memoryEntry
	q := s.db.Where("app_name = ? AND user_id = ?", req.AppName, req.UserID)
	keywords := strings.Fields(strings.ToLower(strings.TrimSpace(req.Query)))
	if len(keywords) > 0 {
		conditions := make([]string, 0, len(keywords))
		args := make([]any, 0, len(keywords))
		for _, kw := range keywords {
			// 转义 LIKE 通配符，使 "100%" 匹配字面文本（与会话内容搜索相同的约定）。
			esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(kw)
			conditions = append(conditions, "lower(content) LIKE ? ESCAPE '\\'")
			args = append(args, "%"+esc+"%")
		}
		q = q.Where("("+strings.Join(conditions, " OR ")+")", args...)
	}
	if err := q.Order("timestamp DESC").Limit(200).Find(&rows).Error; err != nil {
		return nil, err
	}

	type scored struct {
		row   memoryEntry
		score int
	}
	var scoredRows []scored
	for _, r := range rows {
		lower := strings.ToLower(r.Content)
		score := 0
		for _, kw := range keywords {
			score += strings.Count(lower, kw)
		}
		if len(keywords) == 0 || score > 0 {
			scoredRows = append(scoredRows, scored{r, score})
		}
	}
	sort.Slice(scoredRows, func(i, j int) bool {
		if scoredRows[i].score != scoredRows[j].score {
			return scoredRows[i].score > scoredRows[j].score
		}
		return scoredRows[i].row.Timestamp.After(scoredRows[j].row.Timestamp)
	})
	if len(scoredRows) > 20 {
		scoredRows = scoredRows[:20]
	}

	resp := &memory.SearchResponse{Memories: []memory.Entry{}}
	for _, sr := range scoredRows {
		var content genai.Content
		if err := json.Unmarshal([]byte(sr.row.Content), &content); err != nil {
			continue
		}
		resp.Memories = append(resp.Memories, memory.Entry{
			ID:        sr.row.ID,
			Content:   &content,
			Author:    sr.row.Author,
			Timestamp: sr.row.Timestamp,
		})
	}
	return resp, nil
}

// Count 返回某用户已存储的记忆数量（诊断/UI 用）。
func (s *SQLiteService) Count(appName, userID string) (int64, error) {
	var n int64
	err := s.db.Model(&memoryEntry{}).Where("app_name = ? AND user_id = ?", appName, userID).Count(&n).Error
	return n, err
}

// Clear 移除某用户的全部记忆。
func (s *SQLiteService) Clear(appName, userID string) error {
	return s.db.Where("app_name = ? AND user_id = ?", appName, userID).Delete(&memoryEntry{}).Error
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
