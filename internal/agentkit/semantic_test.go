package agentkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmate/internal/config"
)

// openTestKnowledgeIndex fakeEmbeddings 通过确定性的词袋哈希向量，提供 OpenAI
// 兼容的 /embeddings 契约：共享词表 =>
// 高余弦相似度，这足以在没有真实模型的情况下覆盖排序、缓存和重建路径。
// openTestKnowledgeIndex 打开一个由临时目录支持的索引，并注册数据库关闭操作，以便测试的 TempDir 清理在
// Windows 上不会失败（打开的句柄会阻塞 RemoveAll）。
func openTestKnowledgeIndex(t *testing.T) (*KnowledgeIndex, error) {
	t.Helper()
	idx, err := OpenKnowledgeIndex(filepath.Join(t.TempDir(), "k.db"))
	if err == nil {
		t.Cleanup(func() {
			if sqlDB, err := idx.DB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		})
	}
	return idx, err
}

func fakeEmbeddings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		type emb struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		}
		data := make([]emb, len(req.Input))
		for i, in := range req.Input {
			vec := make([]float64, 64)
			for _, w := range strings.Fields(strings.ToLower(in)) {
				h := fnv.New32a()
				_, _ = h.Write([]byte(w))
				vec[h.Sum32()%64] += 1
			}
			var sumsq float64
			for _, v := range vec {
				sumsq += v * v
			}
			sq := sqrtApprox(sumsq)
			if sq == 0 {
				sq = 1
			}
			for j := range vec {
				vec[j] /= sq
			}
			data[i] = emb{Object: "embedding", Index: i, Embedding: vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   data,
			"model":  "fake-embed",
			"usage":  map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		})
	}
}

func sqrtApprox(x float64) float64 {
	// 牛顿法；向量很小，因此精度无关紧要。
	if x == 0 {
		return 0
	}
	g := x
	for i := 0; i < 40; i++ {
		g = (g + x/g) / 2
	}
	return g
}

func seedKnowledge(t *testing.T, files map[string]string) string {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, "knowledge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// TestDeletedKnowledgeDirClearsIndex：删除整个 knowledge
// 目录必须清空索引——过去，过期向量会残留，搜索会不断返回已不存在文件的内容。
func TestDeletedKnowledgeDirClearsIndex(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{
		"k8s.md": "# 集群\n\nkubernetes pod 调度策略与资源配额管理。",
	})
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}}
	if _, err := searchHybrid(opts, "kubernetes"); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, idx) == 0 {
		t.Fatal("precondition: indexed rows expected")
	}
	if err := os.RemoveAll(filepath.Join(ws, "knowledge")); err != nil {
		t.Fatal(err)
	}
	res, err := searchHybrid(opts, "kubernetes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "知识库为空") {
		t.Fatalf("search must report an empty knowledge base after dir removal, got: %s", res)
	}
	if got := rowCount(t, idx); got != 0 {
		t.Fatalf("index must be cleared after dir removal, rows=%d", got)
	}
}

// TestPartialEmbeddingFailureKeepsGoodRows: one file failing to embed must
// not discard the rows of the files that succeeded — a single network blip
// used to throw away the whole rebuild.
func TestPartialEmbeddingFailureKeepsGoodRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, in := range req.Input {
			if strings.Contains(in, "必然失败") {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
		}
		fakeEmbeddings()(w, r)
	}))
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{
		"good.md": "kubernetes pod 调度策略与资源配额管理。",
		"bad.md":  "这个文件必然失败：触发嵌入错误的标记。",
	})
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}}
	// 该工具在出错时会降级为关键词搜索；我们锁定的是 INDEX 状态：good.md
	// 的记录行必须在 bad.md 失败后保留，并且错误必须指出失败的文件以便诊断。
	if err := idx.ensureIndexed(context.Background(), ws, opts.Embedding); err == nil {
		t.Fatal("expected an aggregate embedding error")
	} else if !strings.Contains(err.Error(), "bad.md") {
		t.Fatalf("error must name the failed file, got: %v", err)
	}
	var goodRows int64
	if err := idx.DB.Model(&KnowledgeChunk{}).Where("file = ?", "good.md").Count(&goodRows).Error; err != nil {
		t.Fatal(err)
	}
	if goodRows == 0 {
		t.Fatal("successful file lost its rows because another file failed to embed")
	}
	var badRows int64
	if err := idx.DB.Model(&KnowledgeChunk{}).Where("file = ?", "bad.md").Count(&badRows).Error; err != nil {
		t.Fatal(err)
	}
	if badRows != 0 {
		t.Fatalf("failed file must not leave rows behind, got %d", badRows)
	}
}

func TestSemanticSearchRanksAndCaches(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()

	ws := seedKnowledge(t, map[string]string{
		"k8s.md":     "# 集群\n\nkubernetes pod 调度策略与资源配额管理是集群稳定性的关键。",
		"cooking.md": "# 烹饪\n\n红烧肉的做法需要先焯水，再加入冰糖炒出糖色。",
	})
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	emb := &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: emb}

	// 通过包装服务器来统计嵌入输入数量：在搜索后对已存储的记录行进行断言更简单。
	res, err := searchHybrid(opts, "kubernetes pod 调度")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "语义相关度") {
		t.Fatalf("expected semantic output, got: %s", res)
	}
	if !strings.HasPrefix(strings.TrimSpace(res), "### k8s.md") {
		t.Fatalf("expected k8s.md ranked first, got: %s", res)
	}
	if strings.Contains(res, "红烧肉") {
		t.Fatalf("irrelevant file leaked into results: %s", res)
	}

	// 存在已索引的记录行，且第二次搜索会复用它们（不会重新嵌入未更改的文件）：记录
	// 行数稳定，搜索仍然正常工作。
	var rows int64
	if err := idx.DB.Model(&KnowledgeChunk{}).Count(&rows).Error; err != nil || rows == 0 {
		t.Fatalf("expected indexed rows, rows=%d err=%v", rows, err)
	}
	before := rowCount(t, idx)
	if _, err := searchHybrid(opts, "kubernetes pod 调度"); err != nil {
		t.Fatal(err)
	}
	if after := rowCount(t, idx); after != before {
		t.Fatalf("unchanged files must not re-index: %d -> %d", before, after)
	}

	// 更改一个文件仅会重新索引该文件。
	if err := os.WriteFile(filepath.Join(ws, "knowledge", "cooking.md"),
		[]byte("# 烹饪\n\n新版本：红烧肉做法，加豆腐乳更香。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := searchHybrid(opts, "红烧肉 豆腐乳"); err != nil {
		t.Fatal(err)
	}
	if after := rowCount(t, idx); after != before {
		t.Fatalf("same total chunks expected after edit, %d -> %d", before, after)
	}
	var mod int64
	idx.DB.Model(&KnowledgeChunk{}).Where("file = ?", "cooking.md").
		Select("count(*)").Scan(&mod)
	if mod == 0 {
		t.Fatal("cooking.md rows missing after edit")
	}
}

func rowCount(t *testing.T, idx *KnowledgeIndex) int64 {
	t.Helper()
	var n int64
	if err := idx.DB.Model(&KnowledgeChunk{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSemanticShrinkingFileDropsStaleChunks：将文件编辑到更少的分块时，必须删除
// 多余的行。把这些行留下会让索引在每次编辑时增长，并可能暴露出用户已经删除的内
// 容。
func TestSemanticShrinkingFileDropsStaleChunks(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()

	long := "# 长文\n\n" + strings.Repeat("这是第一段很长的说明文字，用于产生多个分块。\n\n", 40)
	ws := seedKnowledge(t, map[string]string{"doc.md": long})
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	emb := &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: emb}

	if _, err := searchHybrid(opts, "说明文字"); err != nil {
		t.Fatal(err)
	}
	before := rowCount(t, idx)
	if before < 2 {
		t.Fatalf("expected several chunks for the long doc, got %d", before)
	}

	if err := os.WriteFile(filepath.Join(ws, "knowledge", "doc.md"), []byte("# 短\n\n只剩一句。"), 0o644); err != nil {
		t.Fatal(err)
	}
	// mtime 具有秒级分辨率；将其调大，使编辑即使在测试与初始索引在同一秒内运行时也可见。
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(ws, "knowledge", "doc.md"), future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := searchHybrid(opts, "说明文字"); err != nil {
		t.Fatal(err)
	}
	if after := rowCount(t, idx); after >= before {
		t.Fatalf("stale chunks survived the edit: %d -> %d", before, after)
	}
}

func TestSemanticModelChangeRebuilds(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{"a.md": "向量检索测试内容 vector search"})
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "model-a"}}
	if _, err := searchHybrid(opts, "向量检索"); err != nil {
		t.Fatal(err)
	}
	first := rowCount(t, idx)

	// 切换模型会清空并重建：行仅携带新模型。
	opts.Embedding.Model = "model-b"
	if _, err := searchHybrid(opts, "向量检索"); err != nil {
		t.Fatal(err)
	}
	if total := rowCount(t, idx); total != first {
		t.Fatalf("rebuild should keep chunk count stable: %d vs %d", first, total)
	}
	var oldModel int64
	idx.DB.Model(&KnowledgeChunk{}).Where("model = ?", "model-a").Count(&oldModel)
	if oldModel != 0 {
		t.Fatalf("stale model rows must be purged, got %d", oldModel)
	}
}

func TestSemanticFallbacks(t *testing.T) {
	ws := seedKnowledge(t, map[string]string{"a.md": "部署手册 deploy guide\n\n部署 deploy 步骤说明。"})

	// 未配置嵌入：纯关键词行为，无标记。
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	res, err := searchHybrid(SearchOptions{Workspace: ws, Index: idx}, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res, "向量检索不可用") || strings.Contains(res, "语义相关度") {
		t.Fatalf("expected keyword-only output, got: %s", res)
	}

	// 嵌入后端出错：优雅地回退到关键词，并附带说明。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	res, err = searchHybrid(SearchOptions{
		Workspace: ws, Index: idx,
		Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "m"},
	}, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "向量检索不可用") || !strings.Contains(res, "deploy") {
		t.Fatalf("expected fallback with note, got: %s", res)
	}
}

func TestChunkText(t *testing.T) {
	long := strings.Repeat("行内容\n", 500) // 1500 个 rune
	chunks := chunkText(long)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := len([]rune(c)); n > chunkRunes {
			t.Fatalf("chunk %d too large: %d", i, n)
		}
		if strings.TrimSpace(c) == "" {
			t.Fatalf("chunk %d empty", i)
		}
	}
	if got := chunkText("短文本"); len(got) != 1 || got[0] != "短文本" {
		t.Fatalf("small text should be one chunk: %v", got)
	}
	if got := chunkText(""); got != nil {
		t.Fatalf("empty text should yield no chunks: %v", got)
	}
}

// TestEmbeddingsProtocolContract 将请求/响应处理固定为 OpenAI 形态：通过 index
// 字段恢复批次顺序，设置认证头。
func TestEmbeddingsProtocolContract(t *testing.T) {
	var gotAuth, gotPath string
	var gotModel string
	var gotInputs int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		gotInputs = len(req.Input)
		// 故意乱序返回；客户端必须根据 index 恢复顺序。
		fmt.Fprint(w, `{"object":"list","data":[`+
			`{"object":"embedding","index":1,"embedding":[0.4,0.0]},`+
			`{"object":"embedding","index":0,"embedding":[0.0,1.0]}],"model":"m","usage":{}}`)
	}))
	defer srv.Close()

	emb := &EmbeddingConfig{BaseURL: srv.URL, APIKey: "sk-test", Model: "m"}
	vecs, err := embedBatchReq(context.Background(), emb, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/embeddings" || gotAuth != "Bearer sk-test" || gotModel != "m" || gotInputs != 2 {
		t.Fatalf("request contract broken: path=%s auth=%s model=%s inputs=%d", gotPath, gotAuth, gotModel, gotInputs)
	}
	if vecs[0][1] != 1.0 || vecs[1][0] != 0.4 {
		t.Fatalf("vectors not restored by index: %v", vecs)
	}
}

func TestKnowledgeIndexStats(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{
		"a.md": "alpha 文件内容 one two",
		"b.md": "beta 文件内容 three",
	})
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}}
	if _, err := searchHybrid(opts, "alpha"); err != nil {
		t.Fatal(err)
	}
	st := idx.Stats()
	if st.Files != 2 || st.Chunks < 2 || st.Model != "fake-embed" {
		t.Fatalf("stats wrong: %+v", st)
	}
	var nilIndex *KnowledgeIndex
	zero := nilIndex.Stats()
	if zero.Chunks != 0 {
		t.Fatalf("nil index stats must be zero: %+v", zero)
	}
}

func TestKnowledgeIndexReset(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{"a.md": "内容 content here"})
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}}
	if _, err := searchHybrid(opts, "内容"); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, idx) == 0 {
		t.Fatal("expected rows before reset")
	}
	if err := idx.Reset(); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, idx); n != 0 {
		t.Fatalf("reset must wipe rows, got %d", n)
	}
	// 下一次搜索会自动重建。
	if _, err := searchHybrid(opts, "内容"); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, idx) == 0 {
		t.Fatal("index did not rebuild after reset")
	}
}

// TestKitTestKnowledgeSearch：设置页调试器路径通过 Kit 解析工作区 +
// 嵌入设置，并返回搜索输出。
func TestKitTestKnowledgeSearch(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := seedKnowledge(t, map[string]string{"a.md": "alpha 内容 here"})
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = ws
		cfg.Settings.EmbeddingProviderID = "p"
		cfg.Settings.EmbeddingModel = "fake-embed"
		cfg.Providers = append(cfg.Providers, config.ModelProvider{ID: "p", Name: "P", Protocol: config.ProtocolOpenAI, BaseURL: srv.URL})
	}); err != nil {
		t.Fatal(err)
	}
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	kit := NewKit(store, nil)
	kit.SetKnowledgeIndex(idx)

	res, err := kit.TestKnowledgeSearch("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "语义相关度") {
		t.Fatalf("expected semantic output: %s", res)
	}
	if _, err := kit.TestKnowledgeSearch("   "); err == nil {
		t.Fatal("empty query should error")
	}
}

// TestSemanticNestedDirectories：语义索引会遍历嵌套的知识目录，并以相对路径作为
// 行的键。
func TestSemanticNestedDirectories(t *testing.T) {
	srv := httptest.NewServer(fakeEmbeddings())
	defer srv.Close()
	ws := t.TempDir()
	nested := filepath.Join(ws, "knowledge", "docs", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "deep.md"), []byte("嵌套目录里的部署手册 deploy"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := openTestKnowledgeIndex(t)
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Workspace: ws, Index: idx, Embedding: &EmbeddingConfig{BaseURL: srv.URL, Model: "fake-embed"}}
	res, err := searchHybrid(opts, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "docs") || !strings.Contains(res, "deep.md") {
		t.Fatalf("nested file missing from refs: %s", res)
	}
}
