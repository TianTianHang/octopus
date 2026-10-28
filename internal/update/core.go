package update

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/bestruirui/octopus/internal/utils/shutdown"
)

func UpdateCore() error {
	log.Infof("start update core")

	filename, err := getDownloadFilename()
	if err != nil {
		log.Warnf("update core failed: %v", err)
		return err
	}

	downloadUrl := updateUrl + "/" + filename
	log.Infof("download url: %s", downloadUrl)
	data, err := doRequestWithFallback(downloadUrl)
	if err != nil {
		log.Warnf("download failed: %v", err)
		return err
	}

	execPath, err := os.Executable()
	if err != nil {
		log.Warnf("get executable path failed: %v", err)
		return err
	}

	if err := applyUpdate(data, execPath); err != nil {
		log.Warnf("apply update failed: %v", err)
		return err
	}

	log.Infof("update core success")
	go restartExecutable(execPath)
	return nil
}

// applyUpdate 把发布归档安装到 execPath, 不触碰同目录的其他文件。
//
// 先解压到临时目录再替换: 直接把归档解压到可执行文件所在目录既会绕过"只换二进制"的意图,
// 也依赖运行中的 exe 可被覆盖——Windows 不允许覆盖正在运行的可执行文件, 但允许改名,
// 因此改为"旧文件改名备份 -> 复制新文件到原路径 -> 失败则回滚"。
func applyUpdate(data []byte, execPath string) error {
	execName := filepath.Base(execPath)
	tmpDir, err := os.MkdirTemp("", execName+"-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := unzip(data, tmpDir); err != nil {
		return fmt.Errorf("unzip: %w", err)
	}

	newExec := filepath.Join(tmpDir, execName)
	info, err := os.Stat(newExec)
	if err != nil || info.IsDir() {
		// 归档布局变了(或二进制被改过名)时明确报错, 而不是留下一个"更新成功"却仍跑旧版本的进程。
		return fmt.Errorf("archive does not contain %s at its root", execName)
	}

	oldPath := execPath + ".old"
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		// 上一次更新遗留的备份在 Windows 上可能仍被占用, 换一个不会冲突的备份名。
		oldPath = fmt.Sprintf("%s.old.%d", execPath, time.Now().Unix())
	}
	if err := os.Rename(execPath, oldPath); err != nil {
		return fmt.Errorf("move current executable aside: %w", err)
	}
	if err := copyFile(newExec, execPath); err != nil {
		// 复制失败时把旧文件放回原位, 保证系统里始终有一个能启动的二进制。
		if restoreErr := os.Rename(oldPath, execPath); restoreErr != nil {
			log.Errorf("restore previous executable failed: %v", restoreErr)
		}
		return fmt.Errorf("install new executable: %w", err)
	}

	// 沿用旧文件权限: 归档里的权限位来自构建机, 不应改变安装位置的可执行权限。
	if oldInfo, statErr := os.Stat(oldPath); statErr == nil {
		_ = os.Chmod(execPath, oldInfo.Mode().Perm())
	}
	// 旧文件可能仍被当前进程占用(Windows), 清理失败不影响本次更新。
	_ = os.Remove(oldPath)
	return nil
}

// copyFile 把 src 复制到 dst, 目标文件已存在时覆盖。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func getDownloadFilename() (string, error) {
	arch := runtime.GOARCH
	goos := runtime.GOOS

	switch goos {
	case "windows":
		switch arch {
		case "386":
			return "octopus-windows-x86.zip", nil
		case "amd64":
			return "octopus-windows-x86_64.zip", nil
		}
	case "darwin":
		switch arch {
		case "amd64":
			return "octopus-darwin-x86_64.zip", nil
		case "arm64":
			return "octopus-darwin-arm64.zip", nil
		}
	case "linux":
		switch arch {
		case "386":
			return "octopus-linux-x86.zip", nil
		case "amd64":
			return "octopus-linux-x86_64.zip", nil
		case "arm":
			return "octopus-linux-armv7.zip", nil
		case "arm64":
			return "octopus-linux-arm64.zip", nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", goos, arch)
}

func restartExecutable(execPath string) {
	shutdown.Shutdown()

	log.Infof("restarting: %q %q", execPath, os.Args[1:])

	if runtime.GOOS == "windows" {
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Errorf("restarting failed: %v", err)
		}
		os.Exit(0)
	}

	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
		log.Errorf("restarting failed: %v", err)
	}
}
