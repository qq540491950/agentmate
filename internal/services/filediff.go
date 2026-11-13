package services

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
)

// 行级 diff 的资源上限：基线内容快照是尽力而为的后台成本，diff 计算是
// 用户点开单个文件时的一次性成本，两者都必须有界。
const (
	diffContentFileMax = 256 << 10 // 基线内容快照的单文件上限
	diffContentBudget  = 32 << 20  // 每会话基线内容总预算
	diffContentFileCap = 2000      // 每会话基线内容文件数上限
	diffMaxFile        = 2 << 20   // diff 读取当前文件的大小上限
	diffDPMaxLines     = 3000      // LCS 动态规划的单侧行数上限，超出走分块
	diffMaxDepth       = 3         // 分块递归的最大深度
	diffChunkLines     = 200       // 分块比较的块行数
	diffMaxOutputLines = 12000     // 单文件 diff 渲染行数上限
	diffContextLines   = 3         // hunk 上下文行数
)

// FileDiffLineType 用于区分差异行，供结果窗格渲染器使用。
type FileDiffLineType string

const (
	DiffLineAdd     FileDiffLineType = "add"
	DiffLineDel     FileDiffLineType = "del"
	DiffLineContext FileDiffLineType = "context"
)

// FileDiffLine 表示一条已渲染的差异行，并带有其从 1 开始的源码行号。
type FileDiffLine struct {
	Type  FileDiffLineType `json:"type"`
	OldNo int              `json:"oldNo,omitempty"`
	NewNo int              `json:"newNo,omitempty"`
	Text  string           `json:"text"`
}

// FileDiffHunk 是一个 unified-diff 块：@@ -oldStart,oldLines +newStart,newLines
// @@。
type FileDiffHunk struct {
	OldStart int            `json:"oldStart"`
	OldLines int            `json:"oldLines"`
	NewStart int            `json:"newStart"`
	NewLines int            `json:"newLines"`
	Lines    []FileDiffLine `json:"lines"`
}

// FileDiffResult 是 FileDiff RPC 的契约。Unsupported
// 非空表示请求有效，但该文件无法进行差异比较（二进制 / 过大 /
// 基线内容缺失）；此时窗格改为内联显示原因。
type FileDiffResult struct {
	SessionID    string                `json:"sessionId"`
	Path         string                `json:"path"`
	Status       WorkspaceChangeStatus `json:"status,omitempty"`
	OldSize      int64                 `json:"oldSize,omitempty"`
	NewSize      int64                 `json:"newSize,omitempty"`
	AddedLines   int                   `json:"addedLines"`
	RemovedLines int                   `json:"removedLines"`
	Hunks        []FileDiffHunk        `json:"hunks"`
	Truncated    bool                  `json:"truncated"`
	Unsupported  string                `json:"unsupported,omitempty"`
}

// looksTextual 对基线快照和当前文件应用相同的判定条件，从而使差异两侧对什么算作文本达成一致
// ：有效 UTF-8，且前导窗口中不含 NUL 字节。
func looksTextual(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	return bytes.IndexByte(head, 0) < 0
}

func splitDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	raw := strings.Split(text, "\n")
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	// 末尾换行会产生一个虚假的末尾空元素（"a\n"
	// 是一行），而真正的末尾空行（"a\n\n" 是两行）会保留。
	if len(lines) > 0 && lines[len(lines)-1] == "" && strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type diffOp struct {
	tag byte // '=', '-', '+'
	i1  int  // 旧范围起点
	i2  int  // 旧范围终点
	j1  int  // 新范围起点
	j2  int  // 新范围终点
}

// lineDiff 将两个行切片进行差异比较，生成带绝对索引的有序操作。先裁剪公共前缀和公共后缀
// ，然后中间部分在足够小时经 LCS DP
// 处理，较大时采用分块划分，从而使内存保持有界。
func lineDiff(a, b []string) []diffOp {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	mid := middleDiff(a[p:len(a)-s], b[p:len(b)-s], diffMaxDepth)
	for i := range mid {
		mid[i].i1 += p
		mid[i].i2 += p
		mid[i].j1 += p
		mid[i].j2 += p
	}
	var ops []diffOp
	if p > 0 {
		ops = append(ops, diffOp{tag: '=', i2: p, j2: p})
	}
	ops = append(ops, mid...)
	if s > 0 {
		oi, nj := len(a)-s, len(b)-s
		ops = append(ops, diffOp{tag: '=', i1: oi, i2: oi + s, j1: nj, j2: nj + s})
	}
	return ops
}

func middleDiff(a, b []string, depth int) []diffOp {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	if len(a) <= diffDPMaxLines && len(b) <= diffDPMaxLines {
		return lcsDiff(a, b)
	}
	return chunkedDiff(a, b, depth)
}

// lcsDiff 是经典的 LCS 动态规划；两侧均以 diffDPMaxLines 为上限，因此该表远低于
// 20 MB。
func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	table := make([][]uint16, n+1)
	buf := make([]uint16, (n+1)*(m+1))
	for i := range table {
		table[i] = buf[i*(m+1) : (i+1)*(m+1)]
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			endI, endJ := i, j
			for endI < n && endJ < m && a[endI] == b[endJ] {
				endI++
				endJ++
			}
			ops = append(ops, diffOp{tag: '=', i1: i, i2: endI, j1: j, j2: endJ})
			i, j = endI, endJ
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, diffOp{tag: '-', i1: i, i2: i + 1, j1: j, j2: j})
			i++
		default:
			ops = append(ops, diffOp{tag: '+', i1: i, i2: i, j1: j, j2: j + 1})
			j++
		}
	}
	if i < n {
		ops = append(ops, diffOp{tag: '-', i1: i, i2: n, j1: j, j2: j})
	}
	if j < m {
		ops = append(ops, diffOp{tag: '+', i1: i, i2: i, j1: j, j2: m})
	}
	return mergeAdjacent(ops)
}

// chunkedDiff 处理超出 DP 上限的中间部分：将其拆分为固定大小的行块，对块键执行
// LCS（两侧按 diffChunkLines
// 收缩），然后递归处理不匹配区域。块键相等意味着原始行逐字节相等，因此相等性扩
// 展是精确的，而非基于哈希概率的。
func chunkedDiff(a, b []string, depth int) []diffOp {
	if depth <= 0 || len(a) == 0 || len(b) == 0 {
		return replaceOp(len(a), len(b))
	}
	blockKeys := func(lines []string) []string {
		count := (len(lines) + diffChunkLines - 1) / diffChunkLines
		keys := make([]string, count)
		for k := range keys {
			start := k * diffChunkLines
			end := start + diffChunkLines
			if end > len(lines) {
				end = len(lines)
			}
			keys[k] = strings.Join(lines[start:end], "\n\x00")
		}
		return keys
	}
	clampA := func(v int) int {
		if v > len(a) {
			return len(a)
		}
		return v
	}
	clampB := func(v int) int {
		if v > len(b) {
			return len(b)
		}
		return v
	}
	// lcsDiff 绝不生成组合的 "replace" 标签：不匹配项会以相邻的 del/add
	// 连续段形式返回。将每个此类连续段归入一个矩形不匹配区域，并递归处理原始行，而
	// 不是对其进行块替换。
	blockOps := lcsDiff(blockKeys(a), blockKeys(b))
	var ops []diffOp
	for start := 0; start < len(blockOps); {
		if op := blockOps[start]; op.tag == '=' {
			ops = append(ops, diffOp{
				tag: '=',
				i1:  clampA(op.i1 * diffChunkLines), i2: clampA(op.i2 * diffChunkLines),
				j1: clampB(op.j1 * diffChunkLines), j2: clampB(op.j2 * diffChunkLines),
			})
			start++
			continue
		}
		end := start
		oi1, oi2, oj1, oj2 := blockOps[start].i1, blockOps[start].i2, blockOps[start].j1, blockOps[start].j2
		for end < len(blockOps) && blockOps[end].tag != '=' {
			// 操作是顺序排列的，因此该连续段的最后一个操作在每一侧承载该区域的远端边界。
			oi2, oj2 = blockOps[end].i2, blockOps[end].j2
			end++
		}
		ao, bo := clampA(oi1*diffChunkLines), clampA(oi2*diffChunkLines)
		bn, bo2 := clampB(oj1*diffChunkLines), clampB(oj2*diffChunkLines)
		for _, sub := range middleDiff(a[ao:bo], b[bn:bo2], depth-1) {
			sub.i1 += ao
			sub.i2 += ao
			sub.j1 += bn
			sub.j2 += bn
			ops = append(ops, sub)
		}
		start = end
	}
	return mergeAdjacent(ops)
}

func replaceOp(oldLen, newLen int) []diffOp {
	var ops []diffOp
	if oldLen > 0 {
		ops = append(ops, diffOp{tag: '-', i2: oldLen})
	}
	if newLen > 0 {
		ops = append(ops, diffOp{tag: '+', j2: newLen})
	}
	return ops
}

// mergeAdjacent 将标签相同的相邻操作合并为一个范围，并丢弃空的上下文连续段。
func mergeAdjacent(ops []diffOp) []diffOp {
	out := ops[:0]
	for _, op := range ops {
		if op.i2 < op.i1 || op.j2 < op.j1 || (op.tag == '=' && op.i2 == op.i1) {
			continue
		}
		if n := len(out); n > 0 && out[n-1].tag == op.tag &&
			out[n-1].i2 == op.i1 && out[n-1].j2 == op.j1 {
			out[n-1].i2 = op.i2
			out[n-1].j2 = op.j2
			continue
		}
		out = append(out, op)
	}
	return out
}

// groupHunks 将 ops 渲染为带 diffContextLines 行上下文的 unified-diff
// hunk，并合并变更组，只要其间隔使它们仍处于同一个 hunk 内。added/removed
// 统计每个 op 行，不受渲染上限影响；truncated 表示 hunk 在 diffMaxOutputLines
// 处被截断。
func groupHunks(ops []diffOp, a, b []string) (hunks []FileDiffHunk, added, removed int, truncated bool) {
	for _, op := range ops {
		switch op.tag {
		case '-':
			removed += op.i2 - op.i1
		case '+':
			added += op.j2 - op.j1
		}
	}
	// 将 ops 展平为带标注的行，同时记录变更行索引。
	var rows []FileDiffLine
	changeIdx := []int{}
	for _, op := range ops {
		switch op.tag {
		case '=':
			for k := op.i1; k < op.i2; k++ {
				rows = append(rows, FileDiffLine{Type: DiffLineContext, OldNo: k + 1, NewNo: op.j1 + (k - op.i1) + 1, Text: a[k]})
			}
		case '-':
			for k := op.i1; k < op.i2; k++ {
				changeIdx = append(changeIdx, len(rows))
				rows = append(rows, FileDiffLine{Type: DiffLineDel, OldNo: k + 1, Text: a[k]})
			}
		case '+':
			for k := op.j1; k < op.j2; k++ {
				changeIdx = append(changeIdx, len(rows))
				rows = append(rows, FileDiffLine{Type: DiffLineAdd, NewNo: k + 1, Text: b[k]})
			}
		}
	}
	if len(changeIdx) == 0 {
		return nil, 0, 0, false
	}
	// 将每个变更扩展为带上下文的 [start,end) 窗口，并合并相接或重叠的窗口。
	type rowRange struct{ start, end int }
	var ranges []rowRange
	cur := rowRange{}
	for _, idx := range changeIdx {
		lo, hi := idx-diffContextLines, idx+diffContextLines+1
		if lo < 0 {
			lo = 0
		}
		if hi > len(rows) {
			hi = len(rows)
		}
		if cur.end > 0 {
			if lo <= cur.end { // 相接的窗口共享上下文：一个 hunk
				if hi > cur.end {
					cur.end = hi
				}
				continue
			}
			ranges = append(ranges, cur)
		}
		cur = rowRange{start: lo, end: hi}
	}
	if cur.end > 0 {
		ranges = append(ranges, cur)
	}
	rendered := 0
	for _, r := range ranges {
		if rendered >= diffMaxOutputLines {
			truncated = true
			break
		}
		lines := rows[r.start:r.end]
		if remaining := diffMaxOutputLines - rendered; len(lines) > remaining {
			lines = lines[:remaining]
			truncated = true
		}
		hunk := FileDiffHunk{Lines: lines}
		for _, l := range hunk.Lines {
			if l.OldNo > 0 && (hunk.OldStart == 0 || l.OldNo < hunk.OldStart) {
				hunk.OldStart = l.OldNo
			}
			if l.NewNo > 0 && (hunk.NewStart == 0 || l.NewNo < hunk.NewStart) {
				hunk.NewStart = l.NewNo
			}
			if l.Type != DiffLineAdd {
				hunk.OldLines++
			}
			if l.Type != DiffLineDel {
				hunk.NewLines++
			}
		}
		hunks = append(hunks, hunk)
		rendered += len(hunk.Lines)
	}
	return hunks, added, removed, truncated
}

func taskContentDir(store *config.Store, sessionID string) string {
	return filepath.Join(taskSnapshotDir(store), "content", filepath.Base(sessionID))
}

func writeTaskContent(store *config.Store, sessionID, relPath string, data []byte) (string, error) {
	sum := sha256.Sum256([]byte(relPath))
	name := hex.EncodeToString(sum[:]) + ".txt"
	dir := taskContentDir(store, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

func readTaskContent(store *config.Store, sessionID, name string) ([]byte, bool) {
	if name == "" {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(taskContentDir(store, sessionID), filepath.Base(name)))
	if err != nil {
		return nil, false
	}
	return data, true
}

func removeTaskContent(store *config.Store, sessionID string) {
	_ = os.RemoveAll(taskContentDir(store, sessionID))
}

// FileDiff 返回单个工作区文件相对于任务基线的行级 diff。新增文件从空内容进行
// diff，删除文件则对基线内容进行
// diff；不受支持的场景（二进制/超大/缺少基线内容）会返回带有 Unsupported
// 原因的结果，而不是错误。
func (a *ArtifactService) FileDiff(sessionID, relPath string) (*FileDiffResult, error) {
	ws, err := a.sessionWorkspace(sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ws) == "" {
		return nil, fmt.Errorf("工作区目录未配置")
	}
	abs, err := agentkit.SafeWorkspacePath(ws, relPath)
	if err != nil {
		return nil, err
	}
	rel := filepath.ToSlash(filepath.Clean(relPath))
	baseline, err := ensureTaskBaseline(a.S.Store, sessionID, ws)
	if err != nil {
		return nil, err
	}
	res := &FileDiffResult{SessionID: sessionID, Path: rel}

	base, baseExisted := baseline.Files[rel]
	baseContent := ""
	if baseExisted {
		res.OldSize = base.Size
		if data, ok := readTaskContent(a.S.Store, sessionID, base.Content); ok {
			baseContent = string(data)
		}
	}

	st, statErr := os.Stat(abs)
	curExists := statErr == nil && !st.IsDir()
	if curExists {
		res.NewSize = st.Size()
	}
	res.Status = changeStatus(baseExisted, curExists, base, st)

	var baseLines, curLines []string
	if baseContent != "" {
		baseLines = splitDiffLines(baseContent)
	}
	if curExists {
		switch {
		case st.Size() > diffMaxFile:
			res.Unsupported = fmt.Sprintf("文件超过 diff 大小上限（%d MB）", diffMaxFile>>20)
			return res, nil
		default:
			data, err := os.ReadFile(abs)
			if err != nil {
				return nil, err
			}
			if !looksTextual(data) {
				res.Unsupported = "二进制文件不支持 diff"
				return res, nil
			}
			curLines = splitDiffLines(string(data))
		}
	}

	if !baseExisted && !curExists {
		return nil, fmt.Errorf("文件 %q 不存在于当前工作区或任务基线中", rel)
	}
	if curExists && baseExisted && baseContent == "" {
		res.Unsupported = "基线未保存该文件内容（建基线时为二进制、空文件或超出快照上限），无法对比"
		return res, nil
	}
	if !curExists && baseContent == "" {
		res.Unsupported = "基线未保存该文件内容，无法展示删除详情"
		return res, nil
	}

	hunks, added, removed, truncated := groupHunks(lineDiff(baseLines, curLines), baseLines, curLines)
	res.AddedLines, res.RemovedLines, res.Truncated = added, removed, truncated
	res.Hunks = hunks
	return res, nil
}

// classifyChange 是快照 diff（buildTaskSnapshot）与 FileDiff
// 共用的唯一文件级分类：与基线条目完全相同的文件不携带状态。
func classifyChange(base taskBaselineFile, existed bool, size int64, modTime time.Time) WorkspaceChangeStatus {
	switch {
	case !existed:
		return ChangeAdded
	case base.Size != size || !base.ModTime.Equal(modTime):
		return ChangeModified
	default:
		return ""
	}
}

// changeStatus 使 classifyChange 适配 FileDiff 的存在性输入。
func changeStatus(baseExisted, curExists bool, base taskBaselineFile, st os.FileInfo) WorkspaceChangeStatus {
	switch {
	case baseExisted && !curExists:
		return ChangeDeleted
	case !baseExisted && curExists:
		return ChangeAdded
	case baseExisted && curExists:
		return classifyChange(base, true, st.Size(), st.ModTime())
	default:
		return ""
	}
}
