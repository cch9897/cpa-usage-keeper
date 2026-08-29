package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// addZenMuxSubscriptionMigration 为 zenmux_credentials 增加规范化订阅/限额信息列。
// 全新数据库由建表迁移直接生成完整列，本迁移只负责存量库的 ALTER。
func addZenMuxSubscriptionMigration(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&entities.ZenMuxCredential{}) {
		return nil
	}
	if !tx.Migrator().HasColumn(&entities.ZenMuxCredential{}, "SubscriptionJSON") {
		if err := tx.Migrator().AddColumn(&entities.ZenMuxCredential{}, "SubscriptionJSON"); err != nil {
			return fmt.Errorf("add zenmux_credentials.subscription_json column: %w", err)
		}
	}
	return nil
}
