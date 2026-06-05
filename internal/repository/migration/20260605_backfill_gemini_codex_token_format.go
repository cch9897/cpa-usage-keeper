package migration

import (
	"errors"
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/timeutil"
	"gorm.io/gorm"
)

const geminiCodexBackfillOverviewCheckpointName = "overview"
const geminiCodexBackfillEventBatchSize = 500

type geminiCodexBackfillIdentityKey struct {
	authType entities.UsageIdentityAuthType
	identity string
}

type geminiCodexBackfillTokenDelta struct {
	outputTokens int64
	totalTokens  int64
}

type geminiCodexBackfillOverviewStatKey struct {
	BucketStart time.Time
	APIGroupKey string
	Model       string
	AuthIndex   string
	ModelAlias  string
}

type geminiCodexBackfillAggregateDeltas struct {
	identityDeltas map[int64]geminiCodexBackfillTokenDelta
	hourlyDeltas   map[geminiCodexBackfillOverviewStatKey]geminiCodexBackfillTokenDelta
	dailyDeltas    map[geminiCodexBackfillOverviewStatKey]geminiCodexBackfillTokenDelta
}

func backfillGeminiCodexTokenFormatMigration(tx *gorm.DB) error {
	for _, model := range []any{
		&entities.UsageEvent{},
		&entities.UsageIdentity{},
		&entities.UsageOverviewHourlyStat{},
		&entities.UsageOverviewDailyStat{},
		&entities.UsageOverviewAggregationCheckpoint{},
	} {
		if !tx.Migrator().HasTable(model) {
			return nil
		}
	}

	identityByKey, err := loadGeminiCodexBackfillIdentityLookup(tx)
	if err != nil {
		return err
	}
	overviewLastAggregatedID, err := loadGeminiCodexBackfillOverviewCursor(tx)
	if err != nil {
		return err
	}

	// 明细行逐条修正；聚合表只记录 delta，最后按唯一 key 写一次，避免迁移时反复读写同一统计行。
	aggregateDeltas := newGeminiCodexBackfillAggregateDeltas()
	lastEventID := int64(0)
	for {
		events := make([]entities.UsageEvent, 0, geminiCodexBackfillEventBatchSize)
		if err := tx.Where("id > ? AND reasoning_tokens > ?", lastEventID, 0).
			Order("id asc").
			Limit(geminiCodexBackfillEventBatchSize).
			Find(&events).Error; err != nil {
			return err
		}
		if len(events) == 0 {
			return aggregateDeltas.flush(tx)
		}
		for _, event := range events {
			key, hasKey := geminiCodexBackfillIdentityKeyForEvent(event)
			identity, hasIdentity := identityByKey[key]
			if !isGeminiCodexBackfillFamilyEvent(event, identity, hasKey && hasIdentity) {
				continue
			}

			outputDelta, totalDelta := geminiCodexBackfillTokenDeltas(event)
			if outputDelta == 0 && totalDelta == 0 {
				continue
			}
			if err := updateGeminiCodexBackfillUsageEvent(tx, event, outputDelta, totalDelta); err != nil {
				return err
			}
			if event.ID <= overviewLastAggregatedID {
				aggregateDeltas.addOverview(event, outputDelta, totalDelta)
			}
			if hasKey && hasIdentity && event.ID <= identity.LastAggregatedUsageEventID {
				aggregateDeltas.addIdentity(identity.ID, outputDelta, totalDelta)
			}
		}
		lastEventID = events[len(events)-1].ID
	}
}

func newGeminiCodexBackfillAggregateDeltas() geminiCodexBackfillAggregateDeltas {
	return geminiCodexBackfillAggregateDeltas{
		identityDeltas: make(map[int64]geminiCodexBackfillTokenDelta),
		hourlyDeltas:   make(map[geminiCodexBackfillOverviewStatKey]geminiCodexBackfillTokenDelta),
		dailyDeltas:    make(map[geminiCodexBackfillOverviewStatKey]geminiCodexBackfillTokenDelta),
	}
}

func (deltas geminiCodexBackfillAggregateDeltas) addIdentity(identityID int64, outputDelta, totalDelta int64) {
	delta := deltas.identityDeltas[identityID]
	delta.outputTokens += outputDelta
	delta.totalTokens += totalDelta
	deltas.identityDeltas[identityID] = delta
}

func (deltas geminiCodexBackfillAggregateDeltas) addOverview(event entities.UsageEvent, outputDelta, totalDelta int64) {
	key := geminiCodexBackfillOverviewKeyForEvent(event)
	hourlyKey := geminiCodexBackfillOverviewStatKey{
		BucketStart: key.HourBucketStart,
		APIGroupKey: key.APIGroupKey,
		Model:       key.Model,
		AuthIndex:   key.AuthIndex,
		ModelAlias:  key.ModelAlias,
	}
	dailyKey := geminiCodexBackfillOverviewStatKey{
		BucketStart: key.DayBucketStart,
		APIGroupKey: key.APIGroupKey,
		Model:       key.Model,
		AuthIndex:   key.AuthIndex,
		ModelAlias:  key.ModelAlias,
	}
	deltas.addHourly(hourlyKey, outputDelta, totalDelta)
	deltas.addDaily(dailyKey, outputDelta, totalDelta)
}

func (deltas geminiCodexBackfillAggregateDeltas) addHourly(key geminiCodexBackfillOverviewStatKey, outputDelta, totalDelta int64) {
	delta := deltas.hourlyDeltas[key]
	delta.outputTokens += outputDelta
	delta.totalTokens += totalDelta
	deltas.hourlyDeltas[key] = delta
}

func (deltas geminiCodexBackfillAggregateDeltas) addDaily(key geminiCodexBackfillOverviewStatKey, outputDelta, totalDelta int64) {
	delta := deltas.dailyDeltas[key]
	delta.outputTokens += outputDelta
	delta.totalTokens += totalDelta
	deltas.dailyDeltas[key] = delta
}

func (deltas geminiCodexBackfillAggregateDeltas) flush(tx *gorm.DB) error {
	for identityID, delta := range deltas.identityDeltas {
		if err := updateGeminiCodexBackfillUsageIdentity(tx, identityID, delta); err != nil {
			return err
		}
	}
	for key, delta := range deltas.hourlyDeltas {
		if err := updateGeminiCodexBackfillHourlyStats(tx, key, delta); err != nil {
			return err
		}
	}
	for key, delta := range deltas.dailyDeltas {
		if err := updateGeminiCodexBackfillDailyStats(tx, key, delta); err != nil {
			return err
		}
	}
	return nil
}

func loadGeminiCodexBackfillIdentityLookup(tx *gorm.DB) (map[geminiCodexBackfillIdentityKey]entities.UsageIdentity, error) {
	var identities []entities.UsageIdentity
	if err := tx.Find(&identities).Error; err != nil {
		return nil, err
	}
	identityByKey := make(map[geminiCodexBackfillIdentityKey]entities.UsageIdentity, len(identities))
	for _, identity := range identities {
		key := geminiCodexBackfillIdentityKey{authType: identity.AuthType, identity: strings.TrimSpace(identity.Identity)}
		if key.identity == "" {
			continue
		}
		identityByKey[key] = identity
	}
	return identityByKey, nil
}

func loadGeminiCodexBackfillOverviewCursor(tx *gorm.DB) (int64, error) {
	var checkpoint entities.UsageOverviewAggregationCheckpoint
	err := tx.Where("name = ?", geminiCodexBackfillOverviewCheckpointName).First(&checkpoint).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return checkpoint.LastAggregatedUsageEventID, nil
}

func geminiCodexBackfillIdentityKeyForEvent(event entities.UsageEvent) (geminiCodexBackfillIdentityKey, bool) {
	authIndex := strings.TrimSpace(event.AuthIndex)
	if authIndex == "" {
		return geminiCodexBackfillIdentityKey{}, false
	}
	switch strings.ToLower(strings.TrimSpace(event.AuthType)) {
	case "oauth":
		return geminiCodexBackfillIdentityKey{authType: entities.UsageIdentityAuthTypeAuthFile, identity: authIndex}, true
	case "apikey", "api_key":
		return geminiCodexBackfillIdentityKey{authType: entities.UsageIdentityAuthTypeAIProvider, identity: authIndex}, true
	default:
		return geminiCodexBackfillIdentityKey{}, false
	}
}

func isGeminiCodexBackfillFamilyEvent(event entities.UsageEvent, identity entities.UsageIdentity, hasIdentity bool) bool {
	if hasIdentity {
		return isGeminiCodexBackfillFamilyType(identity.Type)
	}
	return isGeminiCodexBackfillFamilyType(event.Provider)
}

func isGeminiCodexBackfillFamilyType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gemini", "vertex", "gemini-cli", "gemini-cli-code-assist", "antigravity", "aistudio", "ai-studio":
		return true
	default:
		return false
	}
}

func geminiCodexBackfillTokenDeltas(event entities.UsageEvent) (int64, int64) {
	if event.ReasoningTokens <= 0 || event.TotalTokens <= 0 {
		return 0, 0
	}
	if event.InputTokens+event.OutputTokens == event.TotalTokens {
		return 0, 0
	}
	if event.InputTokens+event.OutputTokens+event.ReasoningTokens != event.TotalTokens {
		return 0, 0
	}
	return event.ReasoningTokens, 0
}

func updateGeminiCodexBackfillUsageEvent(tx *gorm.DB, event entities.UsageEvent, outputDelta, totalDelta int64) error {
	return tx.Model(&entities.UsageEvent{}).
		Where("id = ?", event.ID).
		Updates(map[string]any{
			"output_tokens": event.OutputTokens + outputDelta,
			"total_tokens":  event.TotalTokens + totalDelta,
		}).Error
}

func updateGeminiCodexBackfillUsageIdentity(tx *gorm.DB, identityID int64, delta geminiCodexBackfillTokenDelta) error {
	return tx.Model(&entities.UsageIdentity{}).
		Where("id = ?", identityID).
		Updates(geminiCodexBackfillTokenDeltaUpdates(delta)).Error
}

type geminiCodexBackfillOverviewKey struct {
	HourBucketStart time.Time
	DayBucketStart  time.Time
	APIGroupKey     string
	Model           string
	AuthIndex       string
	ModelAlias      string
}

func geminiCodexBackfillOverviewKeyForEvent(event entities.UsageEvent) geminiCodexBackfillOverviewKey {
	timestamp := timeutil.NormalizeStorageTime(event.Timestamp)
	dayBucket := time.Date(timestamp.Year(), timestamp.Month(), timestamp.Day(), 0, 0, 0, 0, timestamp.Location())
	modelAlias := ""
	if event.ModelAlias != nil {
		modelAlias = normalizeGeminiCodexBackfillOptionalDimension(*event.ModelAlias)
	}
	return geminiCodexBackfillOverviewKey{
		HourBucketStart: timestamp.Truncate(time.Hour),
		DayBucketStart:  dayBucket,
		APIGroupKey:     normalizeGeminiCodexBackfillDimension(event.APIGroupKey),
		Model:           normalizeGeminiCodexBackfillDimension(event.Model),
		AuthIndex:       normalizeGeminiCodexBackfillOptionalDimension(event.AuthIndex),
		ModelAlias:      modelAlias,
	}
}

func normalizeGeminiCodexBackfillDimension(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func normalizeGeminiCodexBackfillOptionalDimension(value string) string {
	return strings.TrimSpace(value)
}

func updateGeminiCodexBackfillHourlyStats(tx *gorm.DB, key geminiCodexBackfillOverviewStatKey, delta geminiCodexBackfillTokenDelta) error {
	return tx.Model(&entities.UsageOverviewHourlyStat{}).
		Where("bucket_start = ? AND api_group_key = ? AND model = ? AND auth_index = ? AND model_alias = ?",
			timeutil.FormatStorageTime(key.BucketStart), key.APIGroupKey, key.Model, key.AuthIndex, key.ModelAlias).
		Updates(geminiCodexBackfillTokenDeltaUpdates(delta)).Error
}

func updateGeminiCodexBackfillDailyStats(tx *gorm.DB, key geminiCodexBackfillOverviewStatKey, delta geminiCodexBackfillTokenDelta) error {
	return tx.Model(&entities.UsageOverviewDailyStat{}).
		Where("bucket_start = ? AND api_group_key = ? AND model = ? AND auth_index = ? AND model_alias = ?",
			timeutil.FormatStorageTime(key.BucketStart), key.APIGroupKey, key.Model, key.AuthIndex, key.ModelAlias).
		Updates(geminiCodexBackfillTokenDeltaUpdates(delta)).Error
}

func geminiCodexBackfillTokenDeltaUpdates(delta geminiCodexBackfillTokenDelta) map[string]any {
	return map[string]any{
		"output_tokens": gorm.Expr("output_tokens + ?", delta.outputTokens),
		"total_tokens":  gorm.Expr("total_tokens + ?", delta.totalTokens),
	}
}
