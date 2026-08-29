package migration

import (
	"path/filepath"
	"testing"

	"cpa-usage-keeper/internal/entities"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAddZenMuxSubscriptionMigrationAddsColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(testSQLiteDSN(filepath.Join(t.TempDir(), "zenmux-subscription.db"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	defer closeOpenedDatabase(t, db)

	if err := createZenMuxCredentialsMigration(db); err != nil {
		t.Fatalf("createZenMuxCredentialsMigration returned error: %v", err)
	}
	// AutoMigrate 会生成当前完整 schema；先回退到无订阅列形态再验证 ALTER 迁移补列。
	if err := db.Exec("ALTER TABLE zenmux_credentials DROP COLUMN subscription_json").Error; err != nil {
		t.Fatalf("drop subscription_json column: %v", err)
	}
	if db.Migrator().HasColumn(&entities.ZenMuxCredential{}, "SubscriptionJSON") {
		t.Fatal("expected subscription_json column to be absent before migration")
	}
	if err := addZenMuxSubscriptionMigration(db); err != nil {
		t.Fatalf("addZenMuxSubscriptionMigration returned error: %v", err)
	}
	if !db.Migrator().HasColumn(&entities.ZenMuxCredential{}, "SubscriptionJSON") {
		t.Fatal("expected subscription_json column to exist after migration")
	}

	// 存量行 subscription_json 保持 NULL。
	if err := db.Create(&entities.ZenMuxCredential{Name: "旧凭证", APIKey: "sk-legacy-key-123", Endpoint: "https://zenmux.ai/api/v1/management/payg/balance"}).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	var row entities.ZenMuxCredential
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("load legacy row: %v", err)
	}
	if row.SubscriptionJSON != nil {
		t.Fatalf("expected subscription_json NULL for legacy row, got %+v", row.SubscriptionJSON)
	}

	// 幂等：重复执行不报错。
	if err := addZenMuxSubscriptionMigration(db); err != nil {
		t.Fatalf("idempotent re-run returned error: %v", err)
	}
}

func TestRunSchemaMigrationRecordsZenMuxSubscriptionMigration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(testSQLiteDSN(filepath.Join(t.TempDir(), "record-zenmux-subscription.db"))), &gorm.Config{})
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

	if err := runSchemaMigration(db, databaseMigration{version: migrationAddZenMuxSubscription, run: addZenMuxSubscriptionMigration}); err != nil {
		t.Fatalf("runSchemaMigration returned error: %v", err)
	}

	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", migrationAddZenMuxSubscription).Count(&count).Error; err != nil {
		t.Fatalf("count migration row: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected migration row count 1, got %d", count)
	}
}
