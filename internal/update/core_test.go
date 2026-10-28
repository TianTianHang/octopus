package update

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildArchive 按发布归档的布局生成一个 zip: 二进制与 README/LICENSE 平铺在根目录。
func buildArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o755)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func writeExecutable(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func TestApplyUpdateReplacesBinaryOnly(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "octopus")
	writeExecutable(t, execPath, "old-binary", 0o750)

	archive := buildArchive(t, map[string]string{
		"octopus":   "new-binary",
		"README.md": "# readme",
		"LICENSE":   "license",
	})
	if err := applyUpdate(archive, execPath); err != nil {
		t.Fatalf("applyUpdate failed: %v", err)
	}

	content, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read replaced executable: %v", err)
	}
	if string(content) != "new-binary" {
		t.Errorf("executable content = %q, want the archive payload", content)
	}
	if _, err := os.Stat(execPath + ".old"); !os.IsNotExist(err) {
		t.Errorf("backup file left behind: %v", err)
	}
	// 归档里的其余文件只应解压到临时目录, 不能落到安装目录。
	for _, name := range []string{"README.md", "LICENSE"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was extracted into the install directory", name)
		}
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(execPath)
		if err != nil {
			t.Fatalf("stat replaced executable: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o750 {
			t.Errorf("mode = %o, want the previous file mode 750", perm)
		}
	}
}

func TestApplyUpdateRejectsArchiveWithoutExecutable(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "octopus")
	writeExecutable(t, execPath, "old-binary", 0o755)

	archive := buildArchive(t, map[string]string{"README.md": "# readme"})
	err := applyUpdate(archive, execPath)
	if err == nil {
		t.Fatal("applyUpdate succeeded on an archive without the executable")
	}
	if !strings.Contains(err.Error(), "does not contain octopus") {
		t.Errorf("error = %v, want a missing-executable message", err)
	}

	content, readErr := os.ReadFile(execPath)
	if readErr != nil {
		t.Fatalf("read executable after failed update: %v", readErr)
	}
	if string(content) != "old-binary" {
		t.Errorf("executable was modified by a failed update: %q", content)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	writeExecutable(t, src, "payload", 0o600)
	writeExecutable(t, dst, "stale", 0o600)

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}
	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(content) != "payload" {
		t.Errorf("dst content = %q, want the source payload", content)
	}
}
