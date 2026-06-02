package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"

	"gorm.io/gorm"
)

func addUsageEventExecutorTypeMigration(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&entities.UsageEvent{}) {
		return nil
	}
	if tx.Migrator().HasColumn(&entities.UsageEvent{}, "executor_type") {
		return nil
	}
	if err := tx.Exec("ALTER TABLE usage_events ADD COLUMN executor_type TEXT NOT NULL DEFAULT ''").Error; err != nil {
		return fmt.Errorf("add usage_events.executor_type column: %w", err)
	}
	return nil
}
