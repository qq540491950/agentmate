package config

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skill.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallSkillZipWithWrapperAndResources(t *testing.T) {
	workspace := t.TempDir()
	zipPath := writeSkillZip(t, map[string]string{
		"diagnosing-bugs/SKILL.md":                "---\nname: diagnosing-bugs\ndescription: Diagnosis loop for hard bugs\n---\n\n# Diagnosing Bugs\n",
		"diagnosing-bugs/references/checklist.md": "checklist",
		"diagnosing-bugs/scripts/run.sh":          "#!/bin/sh\necho ok\n",
		"__MACOSX/diagnosing-bugs/._SKILL.md":     "metadata",
	})

	got, err := InstallSkillZip(zipPath, filepath.Join(workspace, ".agents", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "diagnosing-bugs" {
		t.Fatalf("name = %q", got.Name)
	}
	for _, rel := range []string{"SKILL.md", "references/checklist.md", "scripts/run.sh"} {
		if _, err := os.Stat(filepath.Join(got.Dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}
	discovered := DiscoveredSkills(workspace, "")
	if len(discovered) != 1 || discovered[0].Name != "diagnosing-bugs" || discovered[0].SourceDir != got.Dir {
		t.Fatalf("discovered = %+v", discovered)
	}
}

func TestInstallSkillZipAtRoot(t *testing.T) {
	workspace := t.TempDir()
	zipPath := writeSkillZip(t, map[string]string{
		"SKILL.md": "---\nname: root-skill\ndescription: Root package\n---\n\nBody",
	})
	got, err := InstallSkillZip(zipPath, filepath.Join(workspace, ".agents", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "root-skill" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestInstallSkillZipRejectsUnsafeAndInvalidPackages(t *testing.T) {
	tests := []struct {
		name    string
		entries map[string]string
		want    string
	}{
		{
			name:    "missing manifest",
			entries: map[string]string{"README.md": "no skill"},
			want:    "缺少 SKILL.md",
		},
		{
			name: "path traversal",
			entries: map[string]string{
				"SKILL.md":      "---\nname: safe\ndescription: Safe\n---\n",
				"../escape.txt": "bad",
			},
			want: "目录穿越",
		},
		{
			name: "invalid name",
			entries: map[string]string{
				"SKILL.md": "---\nname: Bad Name\ndescription: Invalid\n---\n",
			},
			want: "校验失败",
		},
		{
			name: "missing description",
			entries: map[string]string{
				"SKILL.md": "---\nname: no-description\n---\n",
			},
			want: "技能描述不能为空",
		},
		{
			name: "multiple manifests",
			entries: map[string]string{
				"a/SKILL.md": "---\nname: a\ndescription: A\n---\n",
				"b/SKILL.md": "---\nname: b\ndescription: B\n---\n",
			},
			want: "多个 SKILL.md",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := t.TempDir()
			zipPath := writeSkillZip(t, tt.entries)
			_, err := InstallSkillZip(zipPath, filepath.Join(workspace, ".agents", "skills"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestInstallSkillZipRejectsSymlinkAndDuplicateTarget(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "already-there"), 0o755); err != nil {
		t.Fatal(err)
	}
	zipPath := writeSkillZip(t, map[string]string{
		"SKILL.md": "---\nname: already-there\ndescription: Existing\n---\n",
	})
	if _, err := InstallSkillZip(zipPath, root); err == nil || !strings.Contains(err.Error(), "已存在同名") {
		t.Fatalf("duplicate target error = %v", err)
	}

	linkZip := filepath.Join(t.TempDir(), "symlink.zip")
	f, err := os.Create(linkZip)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	manifest, _ := zw.Create("SKILL.md")
	_, _ = manifest.Write([]byte("---\nname: linked\ndescription: Linked\n---\n"))
	header := &zip.FileHeader{Name: "scripts/run.sh", Method: zip.Deflate}
	header.SetMode(os.ModeSymlink | 0o777)
	w, _ := zw.CreateHeader(header)
	_, _ = w.Write([]byte("/tmp/outside"))
	_ = zw.Close()
	_ = f.Close()
	if _, err := InstallSkillZip(linkZip, root); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("symlink error = %v", err)
	}
}
