// ログイン失敗回数の制限を扱う。
package server

import (
	"sync"
	"time"
)

type loginLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, fails: map[string][]time.Time{}}
}

// Allow は直近の失敗回数が上限未満のときにログイン試行を許可する。
func (l *loginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = recent(l.fails[key], l.window)
	return len(l.fails[key]) < l.max
}

// Fail は失敗したログイン試行を記録する。
func (l *loginLimiter) Fail(key string) {
	l.mu.Lock()
	l.fails[key] = append(recent(l.fails[key], l.window), time.Now())
	l.mu.Unlock()
}

// Reset はログイン成功後に失敗回数を消す。
func (l *loginLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}

func recent(times []time.Time, window time.Duration) []time.Time {
	cutoff := time.Now().Add(-window)
	kept := times[:0]
	for _, ts := range times {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	return kept
}
