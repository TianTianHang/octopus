package cmd

import (
	"context"
	"os"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/spf13/cobra"
)

var (
	cleanCfgFile   string
	cleanDays      int
	cleanMaxSizeMB int
	cleanAll       bool
	cleanYes       bool
	cleanDryRun    bool
	cleanVacuum    bool
)

var cleanCmd = &cobra.Command{
	Use:     "clean",
	Aliases: []string{"clean-logs", "purge-logs"},
	Short:   "Delete expired relay logs and optionally reclaim disk space",
	Long: "Delete relay_logs rows according to the configured retention period and size cap.\n" +
		"By default it uses relay_log_keep_period / relay_log_max_size_mb from the database.\n" +
		"Use --days / --max-size-mb to override them for this run, --all to delete every log,\n" +
		"and --vacuum to rebuild the SQLite file afterwards (requires free disk space).",
	Example: `  octopus clean --dry-run
  octopus clean --days 3
  octopus clean --max-size-mb 1024 --vacuum
  octopus clean --all --yes --vacuum`,
	Run: func(cmd *cobra.Command, args []string) {
		conf.PrintBanner()
		if err := conf.Load(cleanCfgFile); err != nil {
			log.Errorf("config load error: %v", err)
			os.Exit(1)
		}
		log.Configure(log.Config{
			Level:           conf.AppConfig.Log.Level,
			Format:          conf.AppConfig.Log.Format,
			Caller:          conf.AppConfig.Log.Caller,
			StacktraceLevel: conf.AppConfig.Log.StacktraceLevel,
		})

		if cleanDays < -1 || cleanDays == 0 {
			log.Errorf("--days must be -1 (use setting) or a positive integer")
			os.Exit(1)
		}
		if cleanDays > model.RelayLogMaxKeepPeriodDays {
			log.Errorf("--days must be <= %d", model.RelayLogMaxKeepPeriodDays)
			os.Exit(1)
		}
		if cleanMaxSizeMB < -1 || cleanMaxSizeMB > model.RelayLogMaxSizeMBLimit {
			log.Errorf("--max-size-mb must be between -1 (use setting) and %d", model.RelayLogMaxSizeMBLimit)
			os.Exit(1)
		}
		if cleanAll && (cleanDays != -1 || cleanMaxSizeMB != -1) {
			log.Errorf("--all cannot be combined with --days or --max-size-mb")
			os.Exit(1)
		}
		if cleanAll && !cleanDryRun && !cleanYes {
			log.Errorf("--all deletes every relay log; add --yes to confirm")
			os.Exit(1)
		}
		if cleanVacuum && cleanDryRun {
			log.Errorf("--vacuum cannot be combined with --dry-run")
			os.Exit(1)
		}
		if cleanVacuum && conf.AppConfig.Database.Type != "sqlite" {
			log.Errorf("--vacuum is only supported for sqlite, current type: %s", conf.AppConfig.Database.Type)
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
		beforeRows, err := op.RelayLogCount(ctx)
		if err != nil {
			log.Errorf("count relay logs failed: %v", err)
			os.Exit(1)
		}
		log.Infof("relay logs before cleanup: rows=%d db_size=%s", beforeRows, databaseSizeLabel(path))
		if beforeRows == 0 {
			log.Infof("nothing to clean")
			return
		}

		deletedRows := int64(0)
		if cleanAll {
			if cleanDryRun {
				log.Infof("[dry-run] would delete all %d relay logs", beforeRows)
				return
			}
			if err := op.RelayLogClear(ctx); err != nil {
				log.Errorf("clear relay logs failed: %v", err)
				os.Exit(1)
			}
			deletedRows = beforeRows
		} else {
			result, err := op.RelayLogCleanupWithOptions(ctx, op.RelayLogCleanupOptions{
				KeepPeriodDays: cleanDays,
				MaxSizeMB:      cleanMaxSizeMB,
				DryRun:         cleanDryRun,
			})
			if err != nil {
				log.Errorf("relay log cleanup failed: %v", err)
				os.Exit(1)
			}
			deletedRows = result.DeletedRows
			log.Infof(
				"relay log cleanup%s: keep_period_days=%d max_size=%s expired_rows=%d trimmed_rows=%d total_deleted=%d",
				dryRunLabel(cleanDryRun), result.KeepPeriodDays, maxSizeLabel(result.MaxSizeBytes),
				result.ExpiredRows, result.TrimmedRows, result.DeletedRows,
			)
		}

		if cleanDryRun {
			log.Infof("[dry-run] no rows were deleted")
			return
		}

		if deletedRows > 0 {
			if err := db.IncrementalVacuum(ctx); err != nil {
				log.Warnf("incremental vacuum failed: %v", err)
			}
		}
		if cleanVacuum {
			if err := db.Vacuum(ctx); err != nil {
				log.Errorf("database vacuum failed: %v", err)
				os.Exit(1)
			}
		}

		afterRows, err := op.RelayLogCount(ctx)
		if err != nil {
			log.Errorf("count relay logs failed: %v", err)
			os.Exit(1)
		}
		log.Infof("relay logs after cleanup: rows=%d db_size=%s", afterRows, databaseSizeLabel(path))
		if !cleanVacuum {
			log.Infof("deleted rows are still held in SQLite free pages; run `octopus clean --vacuum` or `octopus compact` to shrink data.db")
		}
	},
}

func init() {
	cleanCmd.Flags().StringVar(&cleanCfgFile, "config", "", "config file (default is ./data/config.json)")
	cleanCmd.Flags().IntVar(&cleanDays, "days", -1, "retention days for this run (-1 = use relay_log_keep_period)")
	cleanCmd.Flags().IntVar(&cleanMaxSizeMB, "max-size-mb", -1, "max live DB size in MB (-1 = use relay_log_max_size_mb, 0 = unlimited)")
	cleanCmd.Flags().BoolVar(&cleanAll, "all", false, "delete all relay logs (requires --yes)")
	cleanCmd.Flags().BoolVar(&cleanYes, "yes", false, "confirm destructive operations")
	cleanCmd.Flags().BoolVar(&cleanDryRun, "dry-run", false, "show what would be deleted without deleting")
	cleanCmd.Flags().BoolVar(&cleanVacuum, "vacuum", false, "run VACUUM after cleanup to reclaim disk space (sqlite only)")
	rootCmd.AddCommand(cleanCmd)
}

func dryRunLabel(dryRun bool) string {
	if dryRun {
		return " [dry-run]"
	}
	return ""
}

func maxSizeLabel(bytes int64) string {
	if bytes <= 0 {
		return "unlimited"
	}
	return formatBytes(bytes)
}

func databaseSizeLabel(path string) string {
	if conf.AppConfig.Database.Type != "sqlite" {
		return "n/a"
	}
	return formatBytes(db.FileSize(path))
}
