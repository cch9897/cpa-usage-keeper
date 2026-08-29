package migration

import (
	"cpa-usage-keeper/internal/entities"

	"gorm.io/gorm"
)

func createZenMuxCredentialsMigration(tx *gorm.DB) error {
	return tx.AutoMigrate(&entities.ZenMuxCredential{})
}
