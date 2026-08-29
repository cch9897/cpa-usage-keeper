package migration

import (
	"path/filepath"
	"testing"

	"cpa-usage-keeper/internal/entities"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAddZenMuxProxyAndBindingMigrationAddsColumns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(testSQLiteDSN(filepath.Join(t.TempDir(), "zenmux-proxy-binding.db"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	defer closeOpenedDatabase(t, db)
	if err := createZenMuxCredentialsMigration(db); err != nil {
		t.Fatalf("createZenMuxCredentialsMigration returned error: %v", err)
	}
	// AutoMigrate 会生成当前完整 schema；先回退到 v1 形态再验证 ALTER 迁移补列。
	for _, column := range []string{"proxy_url", "bound_auth_type"} {
		if err := db.Exec("ALTER TABLE zenmux_credentials DROP COLUMN " + column).Error; err != nil {
			t.Fatalf("drop %s column: %v", column, err)
		}
	}
	if db.Migrator().HasColumn(&entities.ZenMuxCredential{}, "ProxyURL") {
		t.Fatal("expected proxy_url column to be absent before migration")
	}
	if err := addZenMuxProxyAndBindingMigration(db); err != nil {
		t.Fatalf("addZenMuxProxyAndBindingMigration returned error: %v", err)
	}
	if !db.Migrator().HasColumn(&entities.ZenMuxCredential{}, "ProxyURL") {
		t.Fatal("expected proxy_url column to exist after migration")
	}
	if !db.Migrator().HasColumn(&entities.ZenMuxCredential{}, "BoundAuthType") {
		t.Fatal("expected bound_auth_type column to exist after migration")
	}

	// 存量行回填：proxy_url 默认空串，bound_auth_type 保持 NULL。
	if err := db.Create(&entities.ZenMuxCredential{Name: "旧凭证", APIKey: "sk-legacy-key-123", Endpoint: "https://zenmux.ai/api/v1/management/payg/balance"}).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	var row entities.ZenMuxCredential
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("load legacy row: %v", err)
	}
	if row.ProxyURL != "" {
		t.Fatalf("expected proxy_url default empty, got %q", row.ProxyURL)
	}
	if row.BoundAuthType != nil {
		t.Fatalf("expected bound_auth_type NULL for legacy row, got %+v", row.BoundAuthType)
	}

	// 幂等：重复执行不报错。
	if err := addZenMuxProxyAndBindingMigration(db); err != nil {
		t.Fatalf("idempotent re-run returned error: %v", err)
	}
}

func TestRunSchemaMigrationRecordsZenMuxProxyAndBindingMigration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(testSQLiteDSN(filepath.Join(t.TempDir(), "record-zenmux-proxy-binding.db"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open record schema database: %v", err)
	}
	defer closeOpenedDatabase(t, db)
	if err := createSchemaMigrationsTable(db); err != nil {
		t.Fatalf("create schema migrations table: %v", err)
	}
	if err := createZenMuxCredentialsMigration(db); err != nil {
		t.Fatalf("createZenMuxCredentialsMigration returned error: %v", err)
	}

	if err := runSchemaMigration(db, databaseMigration{version: migrationAddZenMuxProxyAndBinding, run: addZenMuxProxyAndBindingMigration}); err != nil {
		t.Fatalf("runSchemaMigration returned error: %v", err)
	}

	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", migrationAddZenMuxProxyAndBinding).Count(&count).Error; err != nil {
		t.Fatalf("count migration row: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected migration row count 1, got %d", count)
	}
}
