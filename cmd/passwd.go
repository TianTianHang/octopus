package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/spf13/cobra"
)

var passwdCfgFile string

var passwdCmd = &cobra.Command{
	Use:   "passwd [password]",
	Short: "Reset the user password without knowing the old one",
	Long: "Overwrite the password of the existing user directly in the database.\n" +
		"This bypasses the old-password check and is meant for local recovery, e.g. when the\n" +
		"admin password is lost and the API no longer accepts a login.\n\n" +
		"Passing the password as an argument leaves it in the shell history and the process\n" +
		"list; omit the argument and pipe the password on stdin to avoid that.\n\n" +
		"The running server keeps the old password in memory, so restart it afterwards.\n" +
		"Tokens issued before the reset stay valid until they expire.",
	Example: `  octopus passwd 'new-password'
  read -rsp 'New password: ' p && printf '%s' "$p" | octopus passwd`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		password, err := resolvePasswdInput(args)
		if err != nil {
			return err
		}
		if err := conf.Load(passwdCfgFile); err != nil {
			return fmt.Errorf("config load error: %w", err)
		}
		log.Configure(log.Config{
			Level:           conf.AppConfig.Log.Level,
			Format:          conf.AppConfig.Log.Format,
			Caller:          conf.AppConfig.Log.Caller,
			StacktraceLevel: conf.AppConfig.Log.StacktraceLevel,
		})
		if err := db.InitDB(conf.AppConfig.Database.Type, conf.AppConfig.Database.Path, conf.IsDebug()); err != nil {
			return fmt.Errorf("database init error: %w", err)
		}
		defer func() { _ = db.Close() }()

		// UserInit 负责把现有用户载入缓存, 顺带覆盖"库还没有用户"的首次运行场景。
		if err := op.UserInit(); err != nil {
			return fmt.Errorf("user init error: %w", err)
		}
		if err := op.UserSetPassword(password); err != nil {
			return err
		}
		cmd.Println("Password updated. Restart the running server to apply the change.")
		return nil
	},
}

// resolvePasswdInput 取得新密码: 有参数时用参数, 否则从标准输入读取一行。
// 交互式终端下不给参数直接报错, 避免静默读取终端输入造成误解。
func resolvePasswdInput(args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}

	stat, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect stdin: %w", err)
	}
	if stat.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("missing password: pass it as an argument or pipe it on stdin")
	}

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	return password, nil
}

func init() {
	passwdCmd.Flags().StringVar(&passwdCfgFile, "config", "", "config file (default is ./data/config.json)")
	rootCmd.AddCommand(passwdCmd)
}
