package plugin

import (
	"net/http"
	"slices"
	"strings"
	"time"
)

// This queue only waits inside the host's interceptor call. No worker or timer
// survives the call. Completion, reconfiguration and slot release wake waiters.
type upstreamWaiter struct{ id, index string }

const maxTotalAccountWaiters = 10000

func (a *App) signalAdmissionsLocked() {
	close(a.admissionSignal)
	a.admissionSignal = make(chan struct{})
}

func (a *App) admitUpstream(req RequestInterceptRequest, admission *requestAdmission) RequestInterceptResponse {
	id := strings.TrimSpace(req.RequestID)
	index := metadataString(req.Metadata, MetadataSelectedIndex)
	_, limit := a.store.ConcurrencySnapshot(index)
	if id == "" || index == "" {
		if limit > 0 {
			return policyErrorResponse(req.SourceFormat, "credential_identity_unavailable", "The host did not report the request ID and credential index")
		}
		return RequestInterceptResponse{}
	}
	a.admissionsMu.Lock()
	defer a.admissionsMu.Unlock()
	if generate, ok := req.Metadata[MetadataGenerate].(bool); ok && !generate {
		a.releaseUpstreamLocked(id)
		return RequestInterceptResponse{}
	}
	if a.upstreamRequests[id] == index {
		return RequestInterceptResponse{}
	}
	a.releaseUpstreamLocked(id)
	if a.upstreamQueued[id] != nil {
		return policyErrorResponse(req.SourceFormat, "request_already_waiting", "This request already has a queue position")
	}
	var waiter *upstreamWaiter
	var timer *time.Timer
	var deadline time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		if waiter != nil {
			a.removeWaiterLocked(waiter)
		}
	}()
	for {
		if a.stopped || admission != nil && admission.completed {
			return policyErrorResponse(req.SourceFormat, "request_completed", "The request has already completed")
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return concurrencyRefusal(req.SourceFormat, "concurrency_queue_timeout", "The request exceeded its concurrency queue timeout.")
		}
		if !a.store.Enabled() {
			return RequestInterceptResponse{}
		}
		cfg, maximum := a.store.ConcurrencySnapshot(index)
		queue := a.upstreamQueues[index]
		first := len(queue) == 0 || waiter != nil && queue[0] == waiter
		if maximum == 0 || a.upstreamCounts[index] < maximum && first {
			a.upstreamRequests[id] = index
			a.upstreamCounts[index]++
			return RequestInterceptResponse{}
		}
		if waiter == nil {
			if cfg.AccountQueueCapacity == 0 {
				return concurrencyRefusal(req.SourceFormat, "upstream_concurrency_limit", "The selected upstream credential has reached its concurrency limit. Retry after an active request completes.")
			}
			if len(queue) >= cfg.AccountQueueCapacity || len(a.upstreamQueued) >= maxTotalAccountWaiters {
				return concurrencyRefusal(req.SourceFormat, "concurrency_queue_full", "The waiting queue is full. Retry after an active request completes.")
			}
			waiter = &upstreamWaiter{id: id, index: index}
			a.upstreamQueues[index] = append(queue, waiter)
			a.upstreamQueued[id] = waiter
			timer = time.NewTimer(time.Duration(cfg.QueueTimeoutSeconds) * time.Second)
			deadline = time.Now().Add(time.Duration(cfg.QueueTimeoutSeconds) * time.Second)
		}
		// The first enqueue starts one deadline; wakeups never extend it.
		wake := a.admissionSignal
		a.admissionsMu.Unlock()
		timedOut := false
		select {
		case <-wake:
		case <-timer.C:
			timedOut = true
		}
		a.admissionsMu.Lock()
		if timedOut {
			return concurrencyRefusal(req.SourceFormat, "concurrency_queue_timeout", "The request exceeded its concurrency queue timeout.")
		}
	}
}

func (a *App) removeWaiterLocked(waiter *upstreamWaiter) {
	queue := a.upstreamQueues[waiter.index]
	if i := slices.Index(queue, waiter); i >= 0 {
		queue = slices.Delete(queue, i, i+1)
		if len(queue) == 0 {
			delete(a.upstreamQueues, waiter.index)
		} else {
			a.upstreamQueues[waiter.index] = queue
		}
	}
	delete(a.upstreamQueued, waiter.id)
	a.signalAdmissionsLocked()
}

func (a *App) releaseUpstreamLocked(id string) {
	id = strings.TrimSpace(id)
	if index, ok := a.upstreamRequests[id]; ok {
		delete(a.upstreamRequests, id)
		a.upstreamCounts[index]--
		if a.upstreamCounts[index] <= 0 {
			delete(a.upstreamCounts, index)
		}
		a.signalAdmissionsLocked()
	}
}

func concurrencyRefusal(format, code, message string) RequestInterceptResponse {
	return RequestInterceptResponse{Terminate: true, StatusCode: http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Content-Type": {"application/json; charset=utf-8"}, "Retry-After": {"1"}},
		ResponseBody:    refusalBody(format, refusal{anthropicType: "rate_limit_error", openaiType: "rate_limit_error", openaiCode: code}, message)}
}
