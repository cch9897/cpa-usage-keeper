package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// addZenMuxProxyAndBindingMigration 为 zenmux_credentials 增加验证代理与绑定身份类型两列。
// 全新数据库由建表迁移直接生成完整列，本迁移只负责存量库的 ALTER。
func addZenMuxProxyAndBindingMigration(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&entities.ZenMuxCredential{}) {
		return nil
	}
	if !tx.Migrator().HasColumn(&entities.ZenMuxCredential{}, "ProxyURL") {
		if err := tx.Migrator().AddColumn(&entities.ZenMuxCredential{}, "ProxyURL"); err != nil {
			return fmt.Errorf("add zenmux_credentials.proxy_url column: %w", err)
		}
	}
	if !tx.Migrator().HasColumn(&entities.ZenMuxCredential{}, "BoundAuthType") {
		if err := tx.Migrator().AddColumn(&entities.ZenMuxCredential{}, "BoundAuthType"); err != nil {
			return fmt.Errorf("add zenmux_credentials.bound_auth_type column: %w", err)
		}
	}
	return nil
}
