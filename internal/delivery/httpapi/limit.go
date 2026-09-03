package httpapi

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrBusy 表示并发已达上限且在等待窗口内未腾出槽位。
var ErrBusy = errors.New("server busy")

// Limiter 是进程内有界并发闸门：最多 maxConcurrent 个持有者，满时在 maxWait 内
// 等待空位（ctx 感知）。属交付层过载保护，领域/应用层不感知（tech-design §7.8）。
type Limiter struct {
	sem     chan struct{}
	maxWait time.Duration
}

// NewLimiter 构造闸门；maxConcurrent<1 归一为 1；maxWait<=0 表示不排队（满即拒）。
func NewLimiter(maxConcurrent int, maxWait time.Duration) *Limiter {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Limiter{sem: make(chan struct{}, maxConcurrent), maxWait: maxWait}
}

// Acquire 取一个槽位：成功返回幂等的 release；满且等待超时→ErrBusy；ctx 取消→ctx.Err()。
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	if l.maxWait <= 0 {
		select {
		case l.sem <- struct{}{}:
			return l.releaser(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, ErrBusy
		}
	}
	timer := time.NewTimer(l.maxWait)
	defer timer.Stop()
	select {
	case l.sem <- struct{}{}:
		return l.releaser(), nil
	case <-timer.C:
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// releaser 返回幂等释放函数（多次调用只释放一次，避免误放多个槽位）。
func (l *Limiter) releaser() func() {
	var once sync.Once
	return func() { once.Do(func() { <-l.sem }) }
}
