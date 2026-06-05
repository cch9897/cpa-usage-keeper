package quota

import (
	"context"
	"sort"
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/timeutil"
)

type InspectionResultStatus string

const (
	InspectionResultStatusNormal             InspectionResultStatus = "normal"
	InspectionResultStatusLimitReached       InspectionResultStatus = "limit_reached"
	InspectionResultStatusUnauthorized401    InspectionResultStatus = "unauthorized_401"
	InspectionResultStatusPaymentRequired402 InspectionResultStatus = "payment_required_402"
	InspectionResultStatusOtherFailed        InspectionResultStatus = "other_failed"
)

type InspectionStatus struct {
	Total              int                `json:"total"`
	Cached             int                `json:"cached"`
	Running            bool               `json:"running"`
	Completed          bool               `json:"completed"`
	CompletedAt        *time.Time         `json:"completed_at,omitempty"`
	Normal             int                `json:"normal"`
	LimitReached       int                `json:"limit_reached"`
	Unauthorized401    int                `json:"unauthorized_401"`
	PaymentRequired402 int                `json:"payment_required_402"`
	OtherFailed        int                `json:"other_failed"`
	Unknown            int                `json:"unknown"`
	Results            []InspectionResult `json:"results"`
}

type InspectionResult struct {
	AuthIndex      string                 `json:"auth_index"`
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`
	FileName       *string                `json:"file_name,omitempty"`
	Status         InspectionResultStatus `json:"status"`
	Error          string                 `json:"error,omitempty"`
	HTTPStatusCode *int                   `json:"http_status_code,omitempty"`
	RefreshedAt    *time.Time             `json:"refreshed_at,omitempty"`
}

func (s *Service) StartInspection(ctx context.Context) (InspectionStatus, error) {
	if s == nil || s.db == nil {
		return InspectionStatus{}, nil
	}
	now := time.Now()
	s.clearSettledRefreshTasks()
	s.resetInspectionRound()
	s.cleanupExpiredRefreshTasks(now)
	summary, err := s.queueAuthFileRefreshRound(ctx, now, authFileRefreshRoundOptions{
		source:              RefreshSourceInspection,
		skipCachedHTTPError: false,
	})
	if err != nil {
		return InspectionStatus{}, err
	}
	s.setInspectionRoundAuthIndexes(summary.roundAuthIndexes)
	if len(summary.queuedAuthIndexes) > 0 {
		if !s.startRefreshGoroutine(func() {
			s.dispatchRefreshTasks(summary.queuedAuthIndexes)
		}) {
			s.markQueuedRefreshTasksFailed(summary.queuedAuthIndexes, context.Canceled)
		}
	}
	return s.GetInspectionStatus(ctx)
}

func (s *Service) GetInspectionStatus(ctx context.Context) (InspectionStatus, error) {
	if s == nil || s.db == nil {
		return InspectionStatus{}, nil
	}
	identities, err := s.listAutoRefreshAuthFiles(ctx)
	if err != nil {
		return InspectionStatus{}, err
	}
	status := InspectionStatus{Total: len(identities)}

	s.cleanupExpiredRefreshTasks(time.Now())
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	for _, identity := range identities {
		task, ok := s.refreshTasks[identity.Identity]
		if !ok {
			continue
		}
		if task.isActive() {
			// 共享刷新缓存里有任意活跃任务时，巡检弹框继续展示“正在刷新”的实时状态。
			status.Running = true
			continue
		}
		result, ok := inspectionResultForTask(identity, task)
		if !ok {
			continue
		}
		// 只有能产出巡检结果的缓存记录才进入 cached；未刷新或 unsupported 统一留给 unknown。
		status.Cached++
		switch result.Status {
		case InspectionResultStatusNormal:
			status.Normal++
		case InspectionResultStatusLimitReached:
			status.LimitReached++
		case InspectionResultStatusUnauthorized401:
			status.Unauthorized401++
		case InspectionResultStatusPaymentRequired402:
			status.PaymentRequired402++
		case InspectionResultStatusOtherFailed:
			status.OtherFailed++
		}
		status.Results = append(status.Results, result)
	}
	sortInspectionResults(status.Results)
	status.Unknown = status.Total - status.Cached
	if status.Unknown < 0 {
		status.Unknown = 0
	}
	if s.inspectionRoundCompletedLocked() {
		status.Completed = true
		// completed_at 是显式巡检轮次的一部分：第一次观察到本轮刷新完成时记录，后续轮询保持稳定。
		if s.inspectionCompletedAt.IsZero() {
			s.inspectionCompletedAt = timeutil.NormalizeStorageTime(time.Now())
		}
		completedAt := s.inspectionCompletedAt
		status.CompletedAt = &completedAt
	}
	return status, nil
}

func (s *Service) clearSettledRefreshTasks() {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	for authIndex, task := range s.refreshTasks {
		if task == nil || task.isActive() {
			continue
		}
		delete(s.refreshTasks, authIndex)
	}
}

func (s *Service) resetInspectionRound() {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.inspectionCompletedAt = time.Time{}
	s.inspectionRoundActive = false
	s.inspectionRoundAuthIndexSet = nil
}

func (s *Service) resetInspectionCompletedAt() {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.inspectionCompletedAt = time.Time{}
}

func (s *Service) setInspectionRoundAuthIndexes(authIndexes []string) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.inspectionRoundActive = true
	s.inspectionRoundAuthIndexSet = make(map[string]struct{}, len(authIndexes))
	for _, authIndex := range authIndexes {
		s.inspectionRoundAuthIndexSet[authIndex] = struct{}{}
	}
}

func (s *Service) inspectionRoundCompletedLocked() bool {
	if !s.inspectionRoundActive {
		return false
	}
	if !s.inspectionCompletedAt.IsZero() {
		return true
	}
	for authIndex := range s.inspectionRoundAuthIndexSet {
		task, ok := s.refreshTasks[authIndex]
		if ok && task.isActive() {
			return false
		}
	}
	return true
}

func inspectionResultForTask(identity entities.UsageIdentity, task *RefreshTaskRecord) (InspectionResult, bool) {
	if task == nil {
		return InspectionResult{}, false
	}
	result := InspectionResult{
		AuthIndex:      identity.Identity,
		Name:           firstNonEmpty(task.Name, identity.Name),
		Type:           firstNonEmpty(task.Type, identity.Type),
		FileName:       task.FileName,
		Error:          task.Error,
		HTTPStatusCode: task.HTTPStatusCode,
	}
	if !task.RefreshedAt.IsZero() {
		refreshedAt := task.RefreshedAt
		result.RefreshedAt = &refreshedAt
	}
	switch task.Status {
	case RefreshTaskStatusCompleted:
		if task.Quota == nil {
			return InspectionResult{}, false
		}
		if inspectionQuotaLimitReached(identity, task, task.Quota.Quota) {
			result.Status = InspectionResultStatusLimitReached
			return result, true
		}
		result.Status = InspectionResultStatusNormal
		return result, true
	case RefreshTaskStatusFailed:
		result.Status = inspectionFailedResultStatus(task.HTTPStatusCode)
		return result, true
	default:
		return InspectionResult{}, false
	}
}

func inspectionFailedResultStatus(statusCode *int) InspectionResultStatus {
	if statusCode == nil {
		return InspectionResultStatusOtherFailed
	}
	switch *statusCode {
	case 401:
		return InspectionResultStatusUnauthorized401
	case 402:
		return InspectionResultStatusPaymentRequired402
	default:
		return InspectionResultStatusOtherFailed
	}
}

func inspectionQuotaLimitReached(identity entities.UsageIdentity, task *RefreshTaskRecord, rows []QuotaRow) bool {
	switch inspectionQuotaProvider(identity, task) {
	case "codex":
		return codexInspectionLimitReached(rows)
	case "claude":
		return claudeInspectionLimitReached(rows)
	case "gemini-cli":
		return geminiCLIInspectionLimitReached(rows)
	case "antigravity":
		return antigravityInspectionLimitReached(rows)
	case "kimi":
		return kimiInspectionLimitReached(rows)
	case "xai":
		return xaiInspectionLimitReached(rows)
	default:
		return false
	}
}

func inspectionQuotaProvider(identity entities.UsageIdentity, task *RefreshTaskRecord) string {
	var taskType string
	if task != nil {
		taskType = task.Type
	}
	for _, value := range []string{taskType, identity.Type} {
		normalized := strings.ToLower(strings.TrimSpace(value))
		switch normalized {
		case "antigravity", "codex", "gemini-cli", "claude", "kimi", "xai":
			return normalized
		}
	}
	return ""
}

func codexInspectionLimitReached(rows []QuotaRow) bool {
	for _, row := range rows {
		if quotaRowLimitReached(row) || quotaRowUsedPercentAtLeast(row, 100) {
			return true
		}
	}
	return false
}

func claudeInspectionLimitReached(rows []QuotaRow) bool {
	for _, row := range rows {
		if quotaRowUsedPercentAtLeast(row, 100) {
			return true
		}
	}
	return false
}

func geminiCLIInspectionLimitReached(rows []QuotaRow) bool {
	for _, row := range rows {
		if quotaRowRemainingFractionAtMost(row, 0) || quotaRowRemainingAtMost(row, 0) {
			return true
		}
	}
	return false
}

func antigravityInspectionLimitReached(rows []QuotaRow) bool {
	for _, row := range rows {
		if quotaRowRemainingFractionAtMost(row, 0) || quotaRowRemainingAtMost(row, 0) {
			return true
		}
	}
	return false
}

func kimiInspectionLimitReached(rows []QuotaRow) bool {
	for _, row := range rows {
		if quotaRowUsedAtLimit(row) || quotaRowRemainingAtMost(row, 0) {
			return true
		}
	}
	return false
}

func xaiInspectionLimitReached(rows []QuotaRow) bool {
	for _, row := range rows {
		if quotaRowLimitReached(row) || quotaRowUsedAtLimit(row) {
			return true
		}
	}
	return false
}

func quotaRowLimitReached(row QuotaRow) bool {
	return row.LimitReached != nil && *row.LimitReached
}

func quotaRowUsedPercentAtLeast(row QuotaRow, threshold float64) bool {
	return row.UsedPercent != nil && *row.UsedPercent >= threshold
}

func quotaRowRemainingFractionAtMost(row QuotaRow, threshold float64) bool {
	return row.RemainingFraction != nil && *row.RemainingFraction <= threshold
}

func quotaRowRemainingAtMost(row QuotaRow, threshold float64) bool {
	return row.Remaining != nil && *row.Remaining <= threshold
}

func quotaRowUsedAtLimit(row QuotaRow) bool {
	return row.Used != nil && row.Limit != nil && *row.Limit > 0 && *row.Used >= *row.Limit
}

func sortInspectionResults(results []InspectionResult) {
	sort.SliceStable(results, func(i, j int) bool {
		left := results[i].RefreshedAt
		right := results[j].RefreshedAt
		switch {
		case left == nil && right == nil:
			return results[i].AuthIndex < results[j].AuthIndex
		case left == nil:
			return false
		case right == nil:
			return true
		default:
			if left.Equal(*right) {
				return results[i].AuthIndex < results[j].AuthIndex
			}
			return left.After(*right)
		}
	})
}
