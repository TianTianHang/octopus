package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/spf13/cobra"
)

var compactCfgFile string

var compactCmd = &cobra.Command{
	Use:   "compact",
	Short: "Reclaim disk space used by the SQLite database",
	Long: "Delete expired request logs, then rebuild the SQLite database file with VACUUM.\n" +
		"This is a maintenance command; stop the running server (or run it in a one-off container)\n" +
		"before executing it. It needs free disk space roughly equal to the current data.db size.",
	Run: func(cmd *cobra.Command, args []string) {
		conf.PrintBanner()
		if err := conf.Load(compactCfgFile); err != nil {
			log.Errorf("config load error: %v", err)
			os.Exit(1)
		}
		log.Configure(log.Config{
			Level:           conf.AppConfig.Log.Level,
			Format:          conf.AppConfig.Log.Format,
			Caller:          conf.AppConfig.Log.Caller,
			StacktraceLevel: conf.AppConfig.Log.StacktraceLevel,
		})

		if conf.AppConfig.Database.Type != "sqlite" {
			log.Errorf("compact only supports sqlite, current type: %s", conf.AppConfig.Database.Type)
			os.Exit(1)
		}
		if err := db.InitDB(conf.AppConfig.Database.Type, conf.AppConfig.Database.Path, conf.IsDebug()); err != nil {
			log.Errorf("database init error: %v", err)
			os.Exit(1)
		}
		defer func() { _ = db.Close() }()

		if err := op.InitCache(); err != nil {
			log.Errorf("cache init error: %v", err)
			os.Exit(1)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()

		path := conf.AppConfig.Database.Path
		before := db.FileSize(path)
		log.Infof("database size before compact: %s", formatBytes(before))

		// 先补齐 relay_logs 的时间/渠道索引；否则容量裁剪会退化成全表扫描，
		// 在 GB 级表上既慢又容易把内存打满。
		if err := op.RelayLogEnsureIndexesSync(ctx); err != nil {
			log.Warnf("ensure relay_logs indexes before compact failed: %v", err)
		}
		// 先按保留期/容量上限删除过期日志，再做全量 VACUUM。
		result, err := op.RelayLogCleanupWithOptions(ctx, op.RelayLogCleanupOptions{MaxSizeMB: -1})
		if err != nil {
			log.Warnf("relay log cleanup before compact failed: %v", err)
		} else if result.DeletedRows > 0 {
			log.Infof("relay log cleanup before compact: deleted_rows=%d", result.DeletedRows)
		}
		if err := db.Vacuum(ctx); err != nil {
			log.Errorf("database compact failed: %v", err)
			os.Exit(1)
		}

		after := db.FileSize(path)
		log.Infof("database compact completed: %s -> %s, freed %s", formatBytes(before), formatBytes(after), formatBytes(before-after))
	},
}

func init() {
	compactCmd.Flags().StringVar(&compactCfgFile, "config", "", "config file (default is ./data/config.json)")
	rootCmd.AddCommand(compactCmd)
}

func formatBytes(bytes int64) string {
	if bytes < 0 {
		return "0 B"
	}
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(bytes)
	for _, unit := range units {
		value /= 1024
		if value < 1024 {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%.1f EiB", value/1024)
}
