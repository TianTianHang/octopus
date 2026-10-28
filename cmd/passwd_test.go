package cmd

import (
	"os"
	"testing"
)

// withStdin 把标准输入替换成一个已写入 input 的管道, 用于覆盖非终端读取分支。
func withStdin(t *testing.T, input string) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe failed: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin fixture failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin fixture failed: %v", err)
	}

	original := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = original
		_ = r.Close()
	})
}

func TestResolvePasswdInputPrefersArgument(t *testing.T) {
	withStdin(t, "from-stdin\n")

	password, err := resolvePasswdInput([]string{"from-arg"})
	if err != nil {
		t.Fatalf("resolvePasswdInput failed: %v", err)
	}
	if password != "from-arg" {
		t.Errorf("password = %q, want the argument value", password)
	}
}

func TestResolvePasswdInputReadsStdin(t *testing.T) {
	withStdin(t, "piped-secret\r\n")

	password, err := resolvePasswdInput(nil)
	if err != nil {
		t.Fatalf("resolvePasswdInput failed: %v", err)
	}
	if password != "piped-secret" {
		t.Errorf("password = %q, want line endings trimmed", password)
	}
}

func TestResolvePasswdInputRejectsEmptyStdin(t *testing.T) {
	withStdin(t, "\n")

	if _, err := resolvePasswdInput(nil); err == nil {
		t.Error("empty stdin accepted as a password")
	}
}
