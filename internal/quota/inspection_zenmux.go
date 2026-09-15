package quota

import (
	"context"
	"sort"
	"time"

	"cpa-usage-keeper/internal/entities"

	"github.com/sirupsen/logrus"
)

// ZenMuxVerifier 是 quota 服务对 ZenMux 批量验证能力的最小依赖；生产由 zenmux.Service 实现。
type ZenMuxVerifier interface {
	VerifyAll(ctx context.Context) error
}

// ZenMuxInspectionResult 是巡检最近结果中单个 ZenMux 凭证的验证快照。
type ZenMuxInspectionResult struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Status       string     `json:"status"`
	Error        string     `json:"error,omitempty"`
	TotalBalance *float64   `json:"total_balance,omitempty"`
	CheckedAt    *time.Time `json:"checked_at,omitempty"`
}

// ZenMuxInspectionStatus 是巡检轮次中 ZenMux 凭证验证部分的状态；结果从 zenmux_credentials 表派生，
// 因此手动单条验证、定时刷新和巡检的结果都会进入 cached/results。
type ZenMuxInspectionStatus struct {
	Total   int                      `json:"total"`
	Cached  int                      `json:"cached"`
	Running bool                     `json:"running"`
	Success int                      `json:"success"`
	Failed  int                      `json:"failed"`
	Unknown int                      `json:"unknown"`
	Results []ZenMuxInspectionResult `json:"results"`
}

// startZenMuxVerifyRound 启动或收养一次 ZenMux 批量验证。
// inspection 来源会把本轮验证计入巡检 running/completed 判定；定时刷新只触发验证，不污染巡检状态。
func (s *Service) startZenMuxVerifyRound(source RefreshSource) {
	if s == nil || s.db == nil || s.zenmuxVerifier == nil {
		return
	}
	s.refreshMu.Lock()
	if s.zenmuxVerifyRunning {
		// 已有批量验证在跑：巡检轮次直接收养它作为本轮 ZenMux 部分，不重复打管理端点。
		if source == RefreshSourceInspection {
			s.inspectionRoundZenmuxPending = true
		}
		s.refreshMu.Unlock()
		return
	}
	s.zenmuxVerifyRunning = true
	if source == RefreshSourceInspection {
		s.inspectionRoundZenmuxPending = true
	}
	s.refreshMu.Unlock()

	if !s.startRefreshGoroutine(func() {
		defer func() {
			s.refreshMu.Lock()
			s.zenmuxVerifyRunning = false
			s.refreshMu.Unlock()
		}()
		// 验证结果由 VerifyAll 逐行持久化，状态读取直接从 DB 派生，这里只需记录系统级失败。
		if err := s.zenmuxVerifier.VerifyAll(s.refreshContextSnapshot()); err != nil {
			logrus.WithError(err).Warn("zenmux verify round failed")
		}
	}) {
		// 应用关闭期间无法派生 goroutine，复位运行标记，避免巡检永远停在 running。
		s.refreshMu.Lock()
		s.zenmuxVerifyRunning = false
		s.refreshMu.Unlock()
	}
}

// zenmuxInspectionRoundRunningLocked 判断 ZenMux 验证是否还应驱动巡检 running。
// 与 Auth Files 同口径：已完成的巡检轮次不能被后续定时验证重新点亮。
func (s *Service) zenmuxInspectionRoundRunningLocked() bool {
	return s.inspectionRoundActive &&
		s.inspectionCompletedAt.IsZero() &&
		s.inspectionRoundZenmuxPending &&
		s.zenmuxVerifyRunning
}

// listZenMuxInspectionCredentials 读取全部 ZenMux 凭证；未配置 verifier 时返回 nil，巡检状态不带 zenmux 块。
func (s *Service) listZenMuxInspectionCredentials(ctx context.Context) ([]entities.ZenMuxCredential, error) {
	if s == nil || s.db == nil || s.zenmuxVerifier == nil {
		return nil, nil
	}
	var rows []entities.ZenMuxCredential
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// buildZenMuxInspectionStatus 由凭证表的持久化验证结果派生巡检展示块；任何来源的验证都计入 cached。
func buildZenMuxInspectionStatus(rows []entities.ZenMuxCredential, running bool) *ZenMuxInspectionStatus {
	status := &ZenMuxInspectionStatus{
		Total:   len(rows),
		Running: running,
		Results: make([]ZenMuxInspectionResult, 0, len(rows)),
	}
	for _, row := range rows {
		if row.CheckedAt == nil {
			// 从未验证的凭证没有可展示结果，统一落到 unknown。
			status.Unknown++
			continue
		}
		status.Cached++
		result := ZenMuxInspectionResult{
			ID:           row.ID,
			Name:         row.Name,
			Status:       row.CheckStatus,
			Error:        row.CheckError,
			TotalBalance: row.TotalBalance,
		}
		checkedAt := *row.CheckedAt
		result.CheckedAt = &checkedAt
		if row.CheckStatus == entities.ZenMuxCredentialCheckStatusSuccess {
			status.Success++
		} else {
			status.Failed++
		}
		status.Results = append(status.Results, result)
	}
	// 与 Auth Files 最近结果同口径：按验证时间倒序，同时间按 id 稳定排序，避免列表跳动。
	sort.SliceStable(status.Results, func(i, j int) bool {
		left, right := status.Results[i], status.Results[j]
		if left.CheckedAt != nil && right.CheckedAt != nil && !left.CheckedAt.Equal(*right.CheckedAt) {
			return left.CheckedAt.After(*right.CheckedAt)
		}
		return left.ID < right.ID
	})
	return status
}
