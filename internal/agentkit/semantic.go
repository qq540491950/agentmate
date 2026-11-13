package agentkit

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// EmbeddingConfig 指向一个兼容 OpenAI 的 /embeddings 端点 （POST
// {base}/embeddings，Bearer 认证，float 编码）。
type EmbeddingConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// KnowledgeChunk 是知识文件中一个被索引的片段。
type KnowledgeChunk struct {
	ID        uint   `gorm:"primaryKey"`
	File      string `gorm:"index:idx_knowledge_file"`
	ModTime   int64  `gorm:"index:idx_knowledge_file"`
	ChunkIdx  int
	Content   string
	Model     string
	Dim       int
	Embedding []byte // 小端序 float32 数组
}

// KnowledgeIndex 是工作区知识文件的持久化向量存储 （位于 sessions.db 旁边的
// SQLite）。为 nil 时禁用语义搜索（关键词回退）。
type KnowledgeIndex struct {
	DB *gorm.DB
	mu sync.Mutex // 串行化（重新）索引
}

// OpenKnowledgeIndex 创建/打开知识索引数据库。
func OpenKnowledgeIndex(path string) (*KnowledgeIndex, error) {
	// 与会话数据库相同的加固措施：WAL + busy_timeout，使并发的
	// 读取方（搜索）与写入方（后台重新索引）绝不会因 "database is locked"
	// 而失败，并只使用单个连接以保证写/读 串行化的确定性。
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := gorm.Open(sqlite.Open(dsn))
	if err != nil {
		return nil, err
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&KnowledgeChunk{}); err != nil {
		return nil, err
	}
	return &KnowledgeIndex{DB: db}, nil
}

const (
	chunkRunes     = 700 // 块大小，以 rune 计
	chunkOverlap   = 100 // 连续分块之间的重叠
	embedBatch     = 32  // 每个 /embeddings 请求的输入数
	semanticTopK   = 8
	semanticWeight = 0.75 // 混合分数：0.75*cosine + 0.25*keyword
	embedWorkers   = 4    // 索引期间并发的 /embeddings 请求
)

// SearchOptions 携带知识工具在运行时所需的一切。
type SearchOptions struct {
	Workspace string
	Index     *KnowledgeIndex  // nil => 仅关键词
	Embedding *EmbeddingConfig // nil => 仅关键词
	// Ctx 限定嵌入 HTTP 调用（索引和查询）；nil 表示
	// context.Background()。知识工具会传入其运行上下文，以便已取消/已超时的回合停
	// 止进行中的嵌入请求。
	Ctx context.Context
}

// searchHybrid 在可能时运行语义搜索，并将其与关键词
// 评分融合；当未配置嵌入后端或嵌入调用失败时，回退到纯关键词搜索。
func searchHybrid(opts SearchOptions, query string) (string, error) {
	if opts.Index == nil || opts.Embedding == nil {
		return searchKnowledge(opts.Workspace, query)
	}
	if opts.Ctx == nil {
		opts.Ctx = context.Background()
	}
	out, err := semanticSearch(opts, query)
	if err != nil {
		// 优雅降级，而不是让模型的工具调用失败。
		res, kwErr := searchKnowledge(opts.Workspace, query)
		if kwErr != nil {
			return "", err
		}
		return "（向量检索不可用，已回退关键词：" + err.Error() + "）\n\n" + res, nil
	}
	return out, nil
}

// semanticSearch 重新索引陈旧文件，嵌入查询，并将余弦
// 相似度与关键词命中融合。嵌入模型发生变化会触发全量
// 重建，因为不同模型的向量不可比较。
func semanticSearch(opts SearchOptions, query string) (string, error) {
	if err := opts.Index.ensureIndexed(opts.Ctx, opts.Workspace, opts.Embedding); err != nil {
		return "", err
	}
	qvec, err := embedOne(opts.Ctx, opts.Embedding, query)
	if err != nil {
		return "", err
	}

	var rows []KnowledgeChunk
	if err := opts.Index.DB.Where("model = ?", opts.Embedding.Model).Find(&rows).Error; err != nil {
		return "", err
	}
	var hits []chunkScore
	keywords := strings.Fields(strings.ToLower(query))
	for _, r := range rows {
		vec := decodeVector(r.Embedding, r.Dim)
		if len(vec) == 0 {
			continue
		}
		hits = append(hits, chunkScore{row: r, cos: cosine(qvec, vec), kw: keywordScore(r.Content, keywords)})
	}
	if len(hits) == 0 {
		return "知识库为空：请将 Markdown/文本文件放入工作区 knowledge/ 目录。", nil
	}
	for i := range hits {
		hits[i].kw = min(hits[i].kw, 10) / 10
	}
	// 相关性下限：丢弃没有有意义信号的分块，这样无关
	// 文件绝不会仅因语料库很小就浮现出来。
	hits = filterByScore(hits)
	if len(hits) == 0 {
		return "知识库中没有找到与查询相关的内容。", nil
	}
	sort.Slice(hits, func(i, j int) bool {
		return blendScore(hits[i]) > blendScore(hits[j])
	})
	if len(hits) > knowledgeMaxResults {
		hits = hits[:knowledgeMaxResults]
	}
	var sb strings.Builder
	filesSet := map[string]bool{}
	for _, h := range hits {
		fmt.Fprintf(&sb, "### %s（语义相关度 %d%%）\n", h.row.File, int(math.Round(max(h.cos, 0)*100)))
		sb.WriteString(snippetOf(h.row.Content, knowledgeSnippet))
		sb.WriteString("\n\n")
		filesSet[h.row.File] = true
	}
	refs := refsOf(filesSet)
	return withRefs(sb.String(), refs), nil
}

// refsOf 返回排序后的 knowledge/<file> 引用，用于预览集成。
func refsOf(files map[string]bool) []string {
	out := make([]string, 0, len(files))
	for f := range files {
		out = append(out, knowledgeDirName+"/"+f)
	}
	sort.Strings(out)
	return out
}

// withRefs 追加一行机器可读的文件引用，UI 用它 提供产物预览按钮。
func withRefs(text string, refs []string) string {
	if len(refs) == 0 {
		return text
	}
	return strings.TrimSpace(text) + "\n\n文件: " + strings.Join(refs, ", ")
}

// ensureIndexed 使持久化索引与知识目录保持同步：
// 重新嵌入已变更的文件（支持嵌套目录，行以相对
// 路径为键），删除已移除的文件，在嵌入模型 变化时重建全部内容。
func (k *KnowledgeIndex) ensureIndexed(ctx context.Context, workspace string, emb *EmbeddingConfig) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	var staleModels int64
	if err := k.DB.Model(&KnowledgeChunk{}).Where("model <> ?", emb.Model).Count(&staleModels).Error; err != nil {
		return err
	}
	if staleModels > 0 {
		if err := k.DB.Where("1 = 1").Delete(&KnowledgeChunk{}).Error; err != nil {
			return err
		}
	}

	dir := filepath.Join(workspace, knowledgeDirName)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			// 整个知识目录已不存在：清空索引，使 搜索不再返回已不存在文件的内容。
			if err := k.DB.Where("1 = 1").Delete(&KnowledgeChunk{}).Error; err != nil {
				return err
			}
			return nil
		}
		return err
	}

	// 仍然存在的文件的 mtime（相对路径支持嵌套）。
	present := map[string]int64{}
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !isKnowledgeFile(d.Name()) {
			return nil
		}
		st, err := d.Info()
		if err != nil || st.Size() > knowledgeMaxFileKB<<10 {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		present[rel] = st.ModTime().Unix()
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	// 丢弃已移除文件对应的行，并记住每个文件最新的 mtime。
	// 同一个文件的行可能跨越多个
	// mod_times（先前的一次编辑可能留下了陈旧行），因此取最大值，而不是任意一行的
	// 值。
	var known []struct {
		File    string
		ModTime int64
	}
	if err := k.DB.Model(&KnowledgeChunk{}).
		Select("file, mod_time").Scan(&known).Error; err != nil {
		return err
	}
	seen := map[string]int64{}
	for _, r := range known {
		if mt, ok := seen[r.File]; !ok || r.ModTime > mt {
			seen[r.File] = r.ModTime
		}
	}
	for f := range seen {
		if _, ok := present[f]; !ok {
			if err := k.DB.Where("file = ?", f).Delete(&KnowledgeChunk{}).Error; err != nil {
				return err
			}
		}
	}

	// 收集需要（重新）嵌入的文件，并清除它们现有的行。
	// 重新嵌入会追加新的行；如果没有这个删除操作，文件缩减（3 chunks ->
	// 2）后会永远保留其多余的行，使索引不断增长，并在每次搜索时返回陈旧内容。
	names := make([]string, 0, len(present))
	for f := range present {
		if old, ok := seen[f]; !ok || old != present[f] {
			names = append(names, f)
		}
	}
	sort.Strings(names)
	for _, f := range names {
		if err := k.DB.Where("file = ?", f).Delete(&KnowledgeChunk{}).Error; err != nil {
			return err
		}
	}

	// 将嵌入工作分派到一个小型工作池（网络受限），然后在此 goroutine
	// 上顺序写入行。
	type embedJob struct {
		file string
		mt   int64
		text []string
		vecs [][]float64
	}
	jobs := make([]embedJob, 0, len(names))
	for _, f := range names {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		jobs = append(jobs, embedJob{file: f, mt: present[f], text: chunkText(string(data))})
	}
	if len(jobs) == 0 {
		return nil
	}
	workers := embedWorkers
	if workers > len(jobs) {
		workers = len(jobs)
	}
	jobsCh := make(chan embedJob)
	results := make(chan embedJob, len(jobs))
	errCh := make(chan error, len(jobs))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobsCh {
				vecs, err := embedAll(ctx, emb, j.text)
				if err != nil {
					errCh <- fmt.Errorf("嵌入 %s 失败: %w", j.file, err)
					continue
				}
				j.vecs = vecs
				results <- j
			}
		}()
	}
	for _, j := range jobs {
		jobsCh <- j
	}
	close(jobsCh)
	wg.Wait()
	close(results)

	// 先写入每个成功的任务：以前只要有一个文件失败，整个重建就会被丢弃。失败的文件
	// 不保留任何行（它们的陈旧行已事先删除），因此下一次搜索只会重试这些文件。
	for r := range results {
		for i, vec := range r.vecs {
			row := KnowledgeChunk{
				File: r.file, ModTime: r.mt, ChunkIdx: i,
				Content: r.text[i], Model: emb.Model,
				Dim: len(vec), Embedding: encodeVector(vec),
			}
			if err := k.DB.Create(&row).Error; err != nil {
				errCh <- fmt.Errorf("写入 %s 失败: %w", r.file, err)
			}
		}
	}
	var errs []string
drain:
	for {
		select {
		case err := <-errCh:
			errs = append(errs, err.Error())
		default:
			break drain
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d 个文件嵌入失败: %s", len(errs), strings.Join(errs, "；"))
	}
	return nil
}

// embedAll 将块拆分成批次，逐批嵌入，并按块顺序拼接向量。
func embedAll(ctx context.Context, emb *EmbeddingConfig, chunks []string) ([][]float64, error) {
	out := make([][]float64, 0, len(chunks))
	for i := 0; i < len(chunks); i += embedBatch {
		end := minInt(i+embedBatch, len(chunks))
		vecs, err := embedBatchReq(ctx, emb, chunks[i:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 嵌入客户端（OpenAI 兼容的 /embeddings，浮点编码）

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// embedBatchReq 嵌入一批输入；响应中的嵌入向量会通过 index 字段按输入顺序返回。
func embedBatchReq(ctx context.Context, emb *EmbeddingConfig, inputs []string) ([][]float64, error) {
	body, _ := json.Marshal(embedRequest{Model: emb.Model, Input: inputs})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(emb.BaseURL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if emb.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+emb.APIKey)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Embeddings 接口返回 HTTP %d: %s", resp.StatusCode, truncateForLog(data))
	}
	var out embedResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(inputs) {
		return nil, fmt.Errorf("Embeddings 返回 %d 个向量（输入 %d 个）", len(out.Data), len(inputs))
	}
	vecs := make([][]float64, len(inputs))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("Embeddings 向量下标 %d 越界", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}

func embedOne(ctx context.Context, emb *EmbeddingConfig, input string) ([]float64, error) {
	vecs, err := embedBatchReq(ctx, emb, []string{input})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// ---------------------------------------------------------------------------
// 向量 + 文本辅助函数

func encodeVector(vec []float64) []byte {
	out := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(float32(v)))
	}
	return out
}

func decodeVector(b []byte, dim int) []float64 {
	if dim <= 0 || len(b) != dim*4 {
		return nil
	}
	out := make([]float64, dim)
	for i := range out {
		out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:])))
	}
	return out
}

// KnowledgeIndexStats 汇总持久化索引，用于可观测性。
type KnowledgeIndexStats struct {
	Files  int64  `json:"files"`
	Chunks int64  `json:"chunks"`
	Model  string `json:"model,omitempty"`
}

// Reset 清除所有缓存的分块；下一次搜索会从工作区文件重新嵌入所有内容。
func (k *KnowledgeIndex) Reset() error {
	if k == nil || k.DB == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.DB.Where("1 = 1").Delete(&KnowledgeChunk{}).Error
}

// Stats 返回文件/分块计数（以及已建立索引时的嵌入模型）。
func (k *KnowledgeIndex) Stats() KnowledgeIndexStats {
	var st KnowledgeIndexStats
	if k == nil || k.DB == nil {
		return st
	}
	k.DB.Model(&KnowledgeChunk{}).Count(&st.Chunks)
	k.DB.Model(&KnowledgeChunk{}).Distinct("file").Count(&st.Files)
	k.DB.Model(&KnowledgeChunk{}).Distinct("model").Pluck("model", &st.Model)
	return st
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// chunkText 将内容切分为重叠的 rune 窗口，优先选择行边界，使分块保持可读。
func chunkText(s string) []string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) == 0 {
		return nil
	}
	var out []string
	for start := 0; start < len(runes); {
		end := minInt(start+chunkRunes, len(runes))
		if end < len(runes) { // 优先在越过中点后的换行符处断行
			for i := end - 1; i > start+chunkRunes/2; i-- {
				if runes[i] == '\n' {
					end = i + 1
					break
				}
			}
		}
		out = append(out, strings.TrimSpace(string(runes[start:end])))
		if end >= len(runes) {
			break
		}
		next := end - chunkOverlap
		if next <= start { // 确保向前推进
			next = start + chunkRunes - chunkOverlap
		}
		start = next
	}
	return out
}

func keywordScore(content string, keywords []string) float64 {
	lower := strings.ToLower(content)
	score := 0.0
	for _, kw := range keywords {
		score += float64(strings.Count(lower, kw))
	}
	return score
}

// chunkScore 将已建立索引的分块与其相似度信号关联起来。
type chunkScore struct {
	row KnowledgeChunk
	cos float64
	kw  float64
}

// blendScore 将语义信号和关键词信号混合成一个排名分数。
func blendScore(h chunkScore) float64 { return semanticWeight*h.cos + 0.25*h.kw }

// filterByScore 保留混合语义 + 关键词分数超过一个较低相关性下限的分块。
func filterByScore(hits []chunkScore) []chunkScore {
	out := hits[:0]
	for _, h := range hits {
		if blendScore(h) >= 0.05 {
			out = append(out, h)
		}
	}
	return out
}

func snippetOf(content string, maxRunes int) string {
	content = strings.TrimSpace(content)
	r := []rune(content)
	if len(r) <= maxRunes {
		return content
	}
	return string(r[:maxRunes]) + "…"
}

func truncateForLog(b []byte) string {
	s := string(b)
	r := []rune(s)
	if len(r) > 200 {
		r = r[:200]
	}
	return strings.ReplaceAll(string(r), "\n", " ")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
