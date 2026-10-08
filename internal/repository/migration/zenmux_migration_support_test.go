package migration

import (
	"strings"
	"testing"

	"gorm.io/gorm"
)

// 上游把所有迁移测试迁到 test/ 子包（外部包，无法访问包内未导出函数），
// fork 保留的 zenmux 迁移用例仍留在包内直接测试未导出迁移函数，因此在此保留这两个数据库助手。

func closeOpenedDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
}

func testSQLiteDSN(path string) string {
	trimmed := strings.TrimSpace(path)
	if strings.Contains(trimmed, "?") {
		return trimmed
	}
	return trimmed + "?_busy_timeout=5000&_foreign_keys=on"
}