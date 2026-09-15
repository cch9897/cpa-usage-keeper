package zenmux

import (
	"context"
	"sync"

	"cpa-usage-keeper/internal/entities"

	"github.com/sirupsen/logrus"
)

// verifyAllWorkerLimit 限制批量验证的并发请求数，避免一次巡检把管理端点打满。
const verifyAllWorkerLimit = 3

// VerifyAll 验证全部凭证并逐个持久化结果；单个凭证失败只记日志，不中断整批。
// 仅供巡检/定时刷新批量入口使用；返回错误仅表示凭证列表读取失败。
func (s *service) VerifyAll(ctx context.Context) error {
	var rows []entities.ZenMuxCredential
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tokens := make(chan struct{}, verifyAllWorkerLimit)
	var wg sync.WaitGroup
	for _, row := range rows {
		select {
		case tokens <- struct{}{}:
		case <-ctx.Done():
			// 关闭期间不再派生新验证；已在跑的验证自行带 ctx 取消，等待它们退出即可。
			wg.Wait()
			return nil
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			defer func() { <-tokens }()
			// Verify 内部把成功/失败持久化到凭证行；返回错误只属于 DB 等系统级失败。
			if _, err := s.Verify(ctx, id); err != nil {
				if ctx.Err() != nil {
					return
				}
				logrus.WithError(err).WithField("zenmux_credential_id", id).Warn("zenmux batch verify failed")
			}
		}(row.ID)
	}
	wg.Wait()
	return nil
}
