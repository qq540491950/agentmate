package config

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	// MaxSkillZipBytes 是 Skill 压缩包的体积上限。解压后的单文件与总量限制
	// 与 agentkit.copySkillResources 保持一致，避免导入一个超过运行时物化上限的包。
	MaxSkillZipBytes = 64 << 20

	maxSkillZipFileBytes  = 10 << 20
	maxSkillZipTotalBytes = 64 << 20
	maxSkillZipFiles      = 5000
	maxSkillMarkdownBytes = 2 << 20
)

// InstalledSkillPackage 描述一次成功的 Skill 包安装结果。
type InstalledSkillPackage struct {
	Name        string
	Description string
	Dir         string
}

type skillZipEntry struct {
	file *zip.File
	name string
	dir  bool
}

// InstallSkillZip 解压并校验一个 skill ZIP 包，然后原子地安装到 targetRoot/<name>。
// 包内可以包含根目录 SKILL.md，也可以包含单层顶层文件夹（例如
// diagnosing-bugs/SKILL.md）；references/、assets/、scripts/ 等附属文件会原样保留。
func InstallSkillZip(zipPath, targetRoot string) (InstalledSkillPackage, error) {
	targetRoot = strings.TrimSpace(targetRoot)
	if targetRoot == "" {
		return InstalledSkillPackage{}, fmt.Errorf("未指定 Skill 安装目录")
	}
	zipPath = strings.TrimSpace(zipPath)
	if zipPath == "" {
		return InstalledSkillPackage{}, fmt.Errorf("未选择 Skill 压缩包")
	}
	info, err := os.Stat(zipPath)
	if err != nil {
		return InstalledSkillPackage{}, fmt.Errorf("读取 Skill 压缩包失败: %w", err)
	}
	if !info.Mode().IsRegular() {
		return InstalledSkillPackage{}, fmt.Errorf("Skill 压缩包不是普通文件")
	}
	if info.Size() > MaxSkillZipBytes {
		return InstalledSkillPackage{}, fmt.Errorf("Skill 压缩包超过 %d MB", MaxSkillZipBytes>>20)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return InstalledSkillPackage{}, fmt.Errorf("Skill 压缩包无法打开: %w", err)
	}
	defer zr.Close()

	manifest, resources, err := inspectSkillZip(zr.File)
	if err != nil {
		return InstalledSkillPackage{}, err
	}
	skillMD, err := readZipEntry(manifest, maxSkillMarkdownBytes)
	if err != nil {
		return InstalledSkillPackage{}, fmt.Errorf("读取 SKILL.md 失败: %w", err)
	}
	name, description, _ := ParseSkillMarkdown(string(skillMD))
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return InstalledSkillPackage{}, fmt.Errorf("SKILL.md 缺少 name（请写入 YAML frontmatter）")
	}
	if err := ValidateSkillMetadata(name, description); err != nil {
		return InstalledSkillPackage{}, fmt.Errorf("SKILL.md 校验失败: %w", err)
	}

	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return InstalledSkillPackage{}, fmt.Errorf("创建 Skill 目录失败: %w", err)
	}
	finalDir := filepath.Join(targetRoot, name)
	if _, err := os.Lstat(finalDir); err == nil {
		return InstalledSkillPackage{}, fmt.Errorf("已存在同名 Skill「%s」，请先删除旧目录或修改名称", name)
	} else if !os.IsNotExist(err) {
		return InstalledSkillPackage{}, fmt.Errorf("检查 Skill 目录失败: %w", err)
	}

	tmpDir, err := os.MkdirTemp(targetRoot, ".import-*")
	if err != nil {
		return InstalledSkillPackage{}, fmt.Errorf("创建 Skill 临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	var total int64
	var files int
	for _, entry := range resources {
		files++
		if files > maxSkillZipFiles {
			return InstalledSkillPackage{}, fmt.Errorf("Skill 包文件数超过 %d 个", maxSkillZipFiles)
		}
		if entry.file.UncompressedSize64 > maxSkillZipFileBytes {
			return InstalledSkillPackage{}, fmt.Errorf("Skill 文件 %q 超过 %d MB", entry.name, maxSkillZipFileBytes>>20)
		}
		if total+int64(entry.file.UncompressedSize64) > maxSkillZipTotalBytes {
			return InstalledSkillPackage{}, fmt.Errorf("Skill 包解压后超过 %d MB", maxSkillZipTotalBytes>>20)
		}
		dst := filepath.Join(tmpDir, filepath.FromSlash(entry.name))
		if err := ensureWithinDir(tmpDir, dst); err != nil {
			return InstalledSkillPackage{}, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return InstalledSkillPackage{}, fmt.Errorf("创建 Skill 子目录失败: %w", err)
		}
		written, err := writeZipEntry(entry.file, dst)
		if err != nil {
			return InstalledSkillPackage{}, fmt.Errorf("解压 %q 失败: %w", entry.name, err)
		}
		total += written
		if total > maxSkillZipTotalBytes {
			return InstalledSkillPackage{}, fmt.Errorf("Skill 包解压后超过 %d MB", maxSkillZipTotalBytes>>20)
		}
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), skillMD, 0o644); err != nil {
		return InstalledSkillPackage{}, fmt.Errorf("写入 SKILL.md 失败: %w", err)
	}
	// MkdirTemp 默认创建 0700 目录；安装后的技能目录应保持普通可读权限。
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return InstalledSkillPackage{}, fmt.Errorf("设置 Skill 目录权限失败: %w", err)
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return InstalledSkillPackage{}, fmt.Errorf("安装 Skill 失败: %w", err)
	}
	return InstalledSkillPackage{Name: name, Description: description, Dir: finalDir}, nil
}

// inspectSkillZip 定位唯一的 SKILL.md，并把其余条目归一化为相对 Skill 根目录的路径。
func inspectSkillZip(entries []*zip.File) (*zip.File, []skillZipEntry, error) {
	var normalized []skillZipEntry
	for _, entry := range entries {
		name, err := normalizeZipEntryName(entry.Name)
		if err != nil {
			return nil, nil, err
		}
		if name == "" || ignoredZipEntry(name) {
			continue
		}
		info := entry.FileInfo()
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("Skill 包包含符号链接「%s」，已拒绝导入", name)
		}
		normalized = append(normalized, skillZipEntry{file: entry, name: name, dir: info.IsDir()})
	}

	var manifests []skillZipEntry
	for _, entry := range normalized {
		if !entry.dir && path.Base(entry.name) == "SKILL.md" {
			manifests = append(manifests, entry)
		} else if strings.EqualFold(path.Base(entry.name), "SKILL.md") {
			return nil, nil, fmt.Errorf("Skill 清单必须命名为大写 SKILL.md，发现「%s」", entry.name)
		}
	}
	if len(manifests) == 0 {
		return nil, nil, fmt.Errorf("Skill 包缺少 SKILL.md")
	}
	if len(manifests) > 1 {
		return nil, nil, fmt.Errorf("Skill 包包含多个 SKILL.md，请只打包一个 Skill")
	}
	manifest := manifests[0]
	prefix := path.Dir(manifest.name)
	if prefix == "." {
		prefix = ""
	}
	if prefix != "" && strings.Contains(prefix, "/") {
		return nil, nil, fmt.Errorf("SKILL.md 只能位于压缩包根目录或单层文件夹内")
	}

	var resources []skillZipEntry
	seen := make(map[string]bool, len(normalized))
	for _, entry := range normalized {
		if entry.name == manifest.name {
			continue
		}
		rel := entry.name
		if prefix != "" {
			if entry.name == prefix {
				continue
			}
			if !strings.HasPrefix(entry.name, prefix+"/") {
				return nil, nil, fmt.Errorf("Skill 包包含 Skill 文件夹之外的内容「%s」", entry.name)
			}
			rel = strings.TrimPrefix(entry.name, prefix+"/")
		}
		if rel == "" || entry.dir {
			continue
		}
		key := strings.ToLower(rel)
		if seen[key] {
			return nil, nil, fmt.Errorf("Skill 包包含重复路径「%s」", rel)
		}
		seen[key] = true
		resources = append(resources, skillZipEntry{file: entry.file, name: rel})
	}
	return manifest.file, resources, nil
}

// normalizeZipEntryName 拒绝绝对路径、盘符与目录穿越，并把压缩包分隔符统一为 /。
func normalizeZipEntryName(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("Skill 包包含非法文件名")
	}
	name := strings.ReplaceAll(raw, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	if name == "" || name == "." {
		return "", nil
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("Skill 包包含目录穿越路径「%s」", raw)
		}
	}
	name = path.Clean(name)
	if strings.HasPrefix(name, "/") || path.IsAbs(name) {
		return "", fmt.Errorf("Skill 包包含绝对路径「%s」", raw)
	}
	if len(name) >= 2 && name[1] == ':' {
		return "", fmt.Errorf("Skill 包包含盘符路径「%s」", raw)
	}
	return name, nil
}

// ignoredZipEntry 跳过 macOS / Windows 资源管理器生成的元数据，避免它们被当成
// Skill 附属文件安装。
func ignoredZipEntry(name string) bool {
	base := path.Base(name)
	return strings.HasPrefix(name, "__MACOSX/") ||
		base == ".DS_Store" || base == "Thumbs.db" ||
		strings.HasPrefix(name, ".git/")
}

// readZipEntry 读取单个条目并限制解压后大小。
func readZipEntry(entry *zip.File, limit int64) ([]byte, error) {
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("文件超过 %d MB", limit>>20)
	}
	return data, nil
}

// writeZipEntry 将条目解压到目标文件，返回实际写入字节数。
func writeZipEntry(entry *zip.File, dst string) (int64, error) {
	rc, err := entry.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	perm := os.FileMode(0o644)
	if entry.Mode()&0o111 != 0 {
		perm = 0o755
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(f, io.LimitReader(rc, maxSkillZipFileBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return written, copyErr
	}
	if closeErr != nil {
		return written, closeErr
	}
	if written > maxSkillZipFileBytes {
		return written, fmt.Errorf("文件超过 %d MB", maxSkillZipFileBytes>>20)
	}
	return written, nil
}

// ensureWithinDir 在路径拼接后再次确认目标仍位于根目录内，防止平台差异绕过
// 前面的字面路径校验。
func ensureWithinDir(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("解析 Skill 文件路径失败: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("Skill 包包含目录穿越路径")
	}
	return nil
}
