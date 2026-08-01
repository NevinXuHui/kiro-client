package reverseproxy

import (
	"sync"
	"sync/atomic"
	"time"
)

type RequestStats struct {
	TotalRequests    int64         `json:"totalRequests"`
	SuccessRequests  int64         `json:"successRequests"`
	FailedRequests   int64         `json:"failedRequests"`
	TotalTokensIn    int64         `json:"totalTokensIn"`
	TotalTokensOut   int64         `json:"totalTokensOut"`
	AvgLatencyMs     int64         `json:"avgLatencyMs"`
	TodayRequests    int64         `json:"todayRequests"`
	StartedAt        time.Time     `json:"startedAt"`
	mu               sync.Mutex
	latencyAcc       int64
	latencyCount     int64
	todayReset       time.Time
}

func NewRequestStats() *RequestStats {
	now := time.Now()
	return &RequestStats{
		StartedAt:  now,
		todayReset: now,
	}
}

func (s *RequestStats) RecordRequest(latency time.Duration, success bool, tokensIn, tokensOut int) {
	atomic.AddInt64(&s.TotalRequests, 1)
	atomic.AddInt64(&s.TodayRequests, 1)
	if success {
		atomic.AddInt64(&s.SuccessRequests, 1)
	} else {
		atomic.AddInt64(&s.FailedRequests, 1)
	}
	atomic.AddInt64(&s.TotalTokensIn, int64(tokensIn))
	atomic.AddInt64(&s.TotalTokensOut, int64(tokensOut))

	s.mu.Lock()
	s.latencyAcc += latency.Milliseconds()
	s.latencyCount++
	if s.latencyCount > 0 {
		s.AvgLatencyMs = s.latencyAcc / s.latencyCount
	}
	s.mu.Unlock()

	if time.Since(s.todayReset) > 24*time.Hour {
		atomic.StoreInt64(&s.TodayRequests, 0)
		s.todayReset = time.Now()
	}
}

func (s *RequestStats) Snapshot() map[string]interface{} {
	return map[string]interface{}{
		"total_requests":   atomic.LoadInt64(&s.TotalRequests),
		"success_requests": atomic.LoadInt64(&s.SuccessRequests),
		"failed_requests":  atomic.LoadInt64(&s.FailedRequests),
		"total_tokens_in":  atomic.LoadInt64(&s.TotalTokensIn),
		"total_tokens_out": atomic.LoadInt64(&s.TotalTokensOut),
		"avg_latency_ms":   atomic.LoadInt64(&s.AvgLatencyMs),
		"today_requests":   atomic.LoadInt64(&s.TodayRequests),
		"started_at":       s.StartedAt.Format(time.RFC3339),
	}
}
