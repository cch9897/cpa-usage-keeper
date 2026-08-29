package migration

import (
	"path/filepath"
	"testing"

	"cpa-usage-keeper/internal/entities"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateZenMuxCredentialsMigrationCreatesTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(testSQLiteDSN(filepath.Join(t.TempDir(), "zenmux-credentials.db"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	defer closeOpenedDatabase(t, db)

	if err := createZenMuxCredentialsMigration(db); err != nil {
		t.Fatalf("createZenMuxCredentialsMigration returned error: %v", err)
	}

	if !db.Migrator().HasTable(&entities.ZenMuxCredential{}) {
		t.Fatalf("expected zenmux_credentials table to exist")
	}
	for _, column := range []string{"name", "api_key", "endpoint", "auth_index", "check_status", "checked_at", "total_balance", "top_up_credits", "bonus_credits", "check_error", "created_at", "updated_at"} {
		if !db.Migrator().HasColumn(&entities.ZenMuxCredential{}, column) {
			t.Fatalf("expected zenmux_credentials.%s column to exist", column)
		}
	}
}

func TestRunSchemaMigrationRecordsZenMuxCredentialsMigration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(testSQLiteDSN(filepath.Join(t.TempDir(), "record-zenmux-credentials.db"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open record schema database: %v", err)
	}
	defer closeOpenedDatabase(t, db)
	if err := createSchemaMigrationsTable(db); err != nil {
		t.Fatalf("create schema migrations table: %v", err)
	}

	if err := runSchemaMigration(db, databaseMigration{version: migrationCreateZenMuxCredentials, run: createZenMuxCredentialsMigration}); err != nil {
		t.Fatalf("runSchemaMigration returned error: %v", err)
	}

	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", migrationCreateZenMuxCredentials).Count(&count).Error; err != nil {
		t.Fatalf("count migration row: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected migration row count 1, got %d", count)
	}
}
