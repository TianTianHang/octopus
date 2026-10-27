package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db/migrate"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB
var sqlitePath string

func InitDB(dbType, dsn string, debug bool) error {
	var err error
	gormConfig := gorm.Config{Logger: logger.Discard}
	if debug {
		gormConfig.Logger = logger.Default.LogMode(logger.Info)
	}

	switch dbType {
	case "sqlite":
		sqlitePath = dsn
		db, err = initSQLite(dsn, &gormConfig)
	case "mysql":
		db, err = initMySQL(dsn, &gormConfig)
	case "postgres", "postgresql":
		db, err = initPostgres(dsn, &gormConfig)
	default:
		return fmt.Errorf("unsupported database type: %s", dbType)
	}

	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	switch dbType {
	case "sqlite":
		// SQLite 单写模型：限制为单连接，避免连接池内自相竞争 SQLITE_BUSY；
		// WAL 模式下读连接由驱动内部处理，不会被该限制阻塞。
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(0)
		sqlDB.SetConnMaxIdleTime(0)
	default:
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetMaxOpenConns(100)
		sqlDB.SetConnMaxLifetime(time.Hour)
		sqlDB.SetConnMaxIdleTime(10 * time.Minute)
	}

	if err := migrate.BeforeAutoMigrate(db); err != nil {
		return err
	}
	// relay_logs 是表里行最大、最容易踩 OOM 的表。但触发 OOM 的根因是
	// glebarez (SQLite) 的 AlterColumn 走 recreateTable 全表拷贝；
	// MySQL/Postgres 的 migrator 用原生 ALTER COLUMN，没有这个问题。
	// 所以这里只在 SQLite 上把 RelayLog 拿出来交给 migrate/013.go 显式处理，
	// MySQL/Postgres 仍然走正常的 smart-migrate，未来加列也能自动跟上。
	models := []interface{}{
		&model.User{},
		&model.Channel{},
		&model.ChannelKey{},
		&model.ProxyConfiguration{},
		&model.Site{},
		&model.SiteAccount{},
		&model.SiteToken{},
		&model.SiteUserGroup{},
		&model.SiteModel{},
		&model.SiteChannelBinding{},
		&model.Group{},
		&model.GroupItem{},
		&model.GroupPreset{},
		&model.LLMInfo{},
		&model.APIKey{},
		&model.Setting{},
		&model.StatsTotal{},
		&model.StatsDaily{},
		&model.StatsHourly{},
		&model.StatsModel{},
		&model.StatsChannel{},
		&model.StatsAPIKey{},
		&model.StatsSiteModelHourly{},
		&model.GroupHealthSnapshot{},
		&model.GroupHealthAttempt{},
		&model.WSResponseAffinity{},
		&model.SiteChannelOutlierState{},
		&migrate.MigrationRecord{},
	}
	if dbType == "sqlite" {
		// SQLite：表不存在时单独 CreateTable（首次安装路径，零行无 OOM 风险）；
		// 表已存在时，用 AddColumn-only 的安全路径补齐缺失字段——这避开了
		// MigrateColumn → AlterColumn → recreateTable 的全表拷贝链路，
		// 同时保留 "未来加新字段不需要手写迁移" 的开发体验。
		// 已有列的类型/默认值漂移由显式 migrate/01x.go 等显式 SQL 处理。
		// success 列的回填以及历史 schema 的一次性整理放在 migrate/013.go。
		// 索引由 op.RelayLogEnsureIndexes 在 server 起来后异步建。
		if !db.Migrator().HasTable(&model.RelayLog{}) {
			if err := db.Migrator().CreateTable(&model.RelayLog{}); err != nil {
				return err
			}
		} else {
			if err := ensureRelayLogColumnsSQLite(db); err != nil {
				return err
			}
		}
	} else {
		// MySQL/Postgres：放心交给 AutoMigrate，原生 ALTER 不会全表拷贝。
		models = append(models, &model.RelayLog{})
	}
	if err := db.AutoMigrate(models...); err != nil {
		return err
	}
	if err := migrate.AfterAutoMigrate(db); err != nil {
		return err
	}
	// Postgres: schema changes during migrations can invalidate cached prepared plans
	// (e.g. "cached plan must not change result type"). Clear them.
	if db.Dialector != nil && db.Dialector.Name() == "postgres" {
		db.Exec("DEALLOCATE ALL")
		db.Exec("DISCARD ALL")
	}
	return nil
}

func initSQLite(path string, config *gorm.Config) (*gorm.DB, error) {
	// glebarez/sqlite (modernc.org/sqlite) 只识别 _pragma=NAME(VALUE) 形式参数，
	// 旧的下划线参数会被静默忽略（导致 WAL/busy_timeout 实际未生效）。
	params := []string{
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		// relay log writer / cleanup 会和请求路径抢同一个 SQLite 写连接，
		// 5s 在 GB 级 relay_logs 上很容易触发 SQLITE_BUSY，导致清理批次失败后
		// 又要等下一轮任务。30s 给批量写入/删除留出足够的重试窗口。
		"_pragma=busy_timeout(30000)",
		"_pragma=foreign_keys(ON)",
		"_pragma=cache_size(-10000)",
		"_pragma=mmap_size(268435456)",
		"_pragma=temp_store(MEMORY)",
		// 新库启用增量 vacuum，删除旧日志后可以把空闲页还给文件系统。
		// 已有库需要执行一次 `octopus compact` 才会切换到该模式。
		"_pragma=auto_vacuum(INCREMENTAL)",
		// 限制 WAL 文件大小，避免 checkpoint 不及时时 -wal 也无限膨胀。
		"_pragma=journal_size_limit(67108864)",
	}
	return gorm.Open(sqlite.Open(path+"?"+strings.Join(params, "&")), config)
}

func initMySQL(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
	if !strings.Contains(dsn, "?") {
		dsn += "?charset=utf8mb4&parseTime=True&loc=Local"
	}
	return gorm.Open(mysql.Open(dsn), config)
}

func initPostgres(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: host=localhost user=postgres password=xxx dbname=octopus port=5432 sslmode=disable
	return gorm.Open(postgres.Open(dsn), config)
}

func Close() error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func GetDB() *gorm.DB {
	return db
}

// Dialect 返回当前数据库方言（sqlite/mysql/postgres），未初始化时返回空串。
func Dialect() string {
	if db == nil || db.Dialector == nil {
		return ""
	}
	return db.Dialector.Name()
}

// Path 返回当前 SQLite 数据库文件路径；非 SQLite 方言返回空串。
func Path() string {
	if Dialect() != "sqlite" {
		return ""
	}
	return sqlitePath
}

// LiveDataSize 估算 SQLite 主库当前“有效数据”占用的字节数。
// page_count 包含空闲页，扣除 freelist_count 后才是仍在使用的页面；
// 用它判断是否需要按容量上限继续裁剪 relay_logs，而不是看文件大小
// （删除行后文件不会立即变小，看文件大小会误判）。
// 非 SQLite 方言返回 0，调用方应跳过容量裁剪。
func LiveDataSize(ctx context.Context) (int64, error) {
	if Dialect() != "sqlite" {
		return 0, nil
	}
	pageCount, err := sqlitePragmaInt(ctx, "page_count")
	if err != nil {
		return 0, fmt.Errorf("read sqlite page_count: %w", err)
	}
	freePages, err := sqlitePragmaInt(ctx, "freelist_count")
	if err != nil {
		return 0, fmt.Errorf("read sqlite freelist_count: %w", err)
	}
	pageSize, err := sqlitePragmaInt(ctx, "page_size")
	if err != nil {
		return 0, fmt.Errorf("read sqlite page_size: %w", err)
	}
	livePages := pageCount - freePages
	if livePages < 0 {
		livePages = 0
	}
	return livePages * pageSize, nil
}

func sqlitePragmaInt(ctx context.Context, name string) (int64, error) {
	var value int64
	row := db.WithContext(ctx).Raw("PRAGMA " + name).Row()
	if err := row.Scan(&value); err != nil {
		return 0, err
	}
	return value, nil
}

// IncrementalVacuum 在 auto_vacuum=INCREMENTAL 的 SQLite 库上把空闲页归还文件系统。
// 老库的 auto_vacuum=0，此时该 PRAGMA 是 no-op，需要执行一次完整的 Vacuum 转换。
func IncrementalVacuum(ctx context.Context) error {
	if Dialect() != "sqlite" {
		return nil
	}
	mode, err := sqlitePragmaInt(ctx, "auto_vacuum")
	if err != nil {
		return fmt.Errorf("read sqlite auto_vacuum: %w", err)
	}
	if mode != 2 {
		return nil
	}
	return db.WithContext(ctx).Exec("PRAGMA incremental_vacuum").Error
}

// Vacuum 重建 SQLite 数据库文件，回收已删除日志占用的磁盘空间。
// 这是一次性维护操作，执行期间会锁住数据库，且需要约等于当前库大小的临时空间。
func Vacuum(ctx context.Context) error {
	if Dialect() != "sqlite" {
		return fmt.Errorf("compact is only supported for sqlite")
	}
	// 老库的 auto_vacuum 是 0；仅在连接上设置该 pragma 不会改变已有库，
	// 必须显式设置后再 VACUUM，VACUUM 会按新设置重建文件。
	if err := db.WithContext(ctx).Exec("PRAGMA auto_vacuum=INCREMENTAL").Error; err != nil {
		return fmt.Errorf("enable incremental auto_vacuum failed: %w", err)
	}
	// 先 checkpoint，避免 WAL 中的页让 VACUUM 复制额外数据。
	if err := db.WithContext(ctx).Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return fmt.Errorf("wal checkpoint failed: %w", err)
	}
	if err := db.WithContext(ctx).Exec("VACUUM").Error; err != nil {
		return fmt.Errorf("vacuum failed: %w", err)
	}
	// VACUUM 本身会写入 WAL；再 checkpoint 一次，命令退出前文件大小才是真实的，
	// 也避免留下一个和主库等大的 -wal 文件。
	if err := db.WithContext(ctx).Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return fmt.Errorf("post-vacuum wal checkpoint failed: %w", err)
	}
	return nil
}

// FileSize 返回数据库主文件及 WAL/SHM 附属文件的字节数。
func FileSize(path string) int64 {
	if path == "" {
		return 0
	}
	var total int64
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	return total
}

// ensureRelayLogColumnsSQLite 用 GORM 的 AddColumn 给 relay_logs 补齐 model 里
// 已声明、但 DB 里还没有的字段。**只**做 ADD COLUMN，不走 smart-migrate 的
// MigrateColumn/AlterColumn 路径——后者在 glebarez/sqlite 上会触发
// recreateTable 全表拷贝，对 GB 级的 relay_logs 表会直接 OOM。
//
// 这条路径让 "未来给 model.RelayLog 加字段不需要手写迁移" 这件事在 SQLite 上
// 也能成立（MySQL/Postgres 走正常的 AutoMigrate）；已有列的类型 / 默认值漂移
// 不会被自动 ALTER，需要显式迁移脚本，但这正是我们想要的——把 "可能引发
// 全表拷贝" 的决定权交给人类。
func ensureRelayLogColumnsSQLite(db *gorm.DB) error {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(&model.RelayLog{}); err != nil {
		return fmt.Errorf("parse relay_logs schema: %w", err)
	}
	for _, field := range stmt.Schema.Fields {
		if field.IgnoreMigration {
			continue
		}
		// 跳过仅在 Go 侧持有的关联字段（没有 DBName）
		if field.DBName == "" {
			continue
		}
		var name string
		if err := db.Raw(
			"SELECT name FROM pragma_table_info('relay_logs') WHERE name = ? LIMIT 1",
			field.DBName,
		).Scan(&name).Error; err != nil {
			return fmt.Errorf("inspect relay_logs column %s: %w", field.DBName, err)
		}
		if name == field.DBName {
			continue
		}
		// AddColumn 在 GORM 里就是 "ALTER TABLE ? ADD ? ?"，
		// 类型由 FullDataTypeOf(field) 生成 —— 直通 ALTER TABLE ADD COLUMN，
		// 绝不会走 recreateTable。
		if err := db.Migrator().AddColumn(&model.RelayLog{}, field.Name); err != nil {
			return fmt.Errorf("add relay_logs column %s: %w", field.DBName, err)
		}
	}
	return nil
}
