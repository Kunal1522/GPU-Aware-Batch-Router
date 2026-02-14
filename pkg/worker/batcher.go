package worker

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/kunal/gpu-batch-router/gen/inference/v1"
	"github.com/kunal/gpu-batch-router/pkg/worker/executor"
)

// BatcherConfig holds tunable batching parameters.
type BatcherConfig struct {
	MaxBatchSize int
	MaxWaitTime  time.Duration
	MinBatchSize int
}

// Batcher implements the adaptive micro-batching engine.
// It collects requests from the priority queue and flushes them
// to the GPU executor when batch is full, timeout fires, or pressure detected.
type Batcher struct {
	cfg    BatcherConfig
	queue  *PriorityQueue
	exec   executor.GPUExecutor
	notify chan struct{} // signals new request arrival
	stopCh chan struct{}
	wg     sync.WaitGroup

	// Adaptive state
	mu          sync.RWMutex
	currentWait time.Duration

	// Metrics (read by metrics collector)
	TotalBatches  atomic.Int64
	TotalRequests atomic.Int64
	LastBatchSize atomic.Int32
	AvgLatencyMs  atomic.Int64 // exponential moving average in microseconds
}

func NewBatcher(cfg BatcherConfig, queue *PriorityQueue, exec executor.GPUExecutor) *Batcher {
	return &Batcher{
		cfg:         cfg,
		queue:       queue,
		exec:        exec,
		notify:      make(chan struct{}, 256),
		stopCh:      make(chan struct{}),
		currentWait: cfg.MaxWaitTime,
	}
}

// Start begins the batching loop in a background goroutine.
func (b *Batcher) Start() {
	b.wg.Add(1)
	go b.loop()
	log.Printf("🔄 Batcher started: max_batch=%d, max_wait=%v, executor=%s",
		b.cfg.MaxBatchSize, b.cfg.MaxWaitTime, b.exec.Name())
}

// Stop gracefully shuts down the batcher.
func (b *Batcher) Stop() {
	close(b.stopCh)
	b.wg.Wait()
}

// Signal notifies the batcher that a new request has arrived.
func (b *Batcher) Signal() {
	select {
	case b.notify <- struct{}{}:
	default:
		// Non-blocking — batcher will pick it up on next iteration
	}
}

func (b *Batcher) loop() {
	defer b.wg.Done()

	for {
		// Wait for at least one request
		select {
		case <-b.stopCh:
			b.drainRemaining()
			return
		case <-b.notify:
		}

		// Collect batch with adaptive timeout
		batch := b.collectBatch()
		if len(batch) == 0 {
			continue
		}

		// Execute the batch
		b.executeBatch(batch)
	}
}

func (b *Batcher) collectBatch() []*PendingRequest {
	b.mu.RLock()
	wait := b.currentWait
	b.mu.RUnlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()

	for {
		depth := b.queue.Depth()

		// Flush if queue has enough for a full batch
		if depth >= b.cfg.MaxBatchSize {
			return b.queue.DequeueN(b.cfg.MaxBatchSize)
		}

		select {
		case <-b.stopCh:
			// Drain what we have on shutdown
			return b.queue.DequeueN(b.cfg.MaxBatchSize)

		case <-timer.C:
			// Timeout — flush whatever we have
			return b.queue.DequeueN(b.cfg.MaxBatchSize)

		case <-b.notify:
			// New request arrived, check if batch is full now
			if b.queue.Depth() >= b.cfg.MaxBatchSize {
				return b.queue.DequeueN(b.cfg.MaxBatchSize)
			}
			// Otherwise keep waiting for more
			continue
		}
	}
}

func (b *Batcher) executeBatch(batch []*PendingRequest) {
	batchSize := len(batch)
	start := time.Now()

	// Extract payloads
	payloads := make([][]byte, batchSize)
	for i, r := range batch {
		payloads[i] = r.Req.Payload
	}

	// Execute on GPU
	results, err := b.exec.ExecuteBatch(payloads)
	elapsed := time.Since(start)

	// Update metrics
	b.TotalBatches.Add(1)
	b.TotalRequests.Add(int64(batchSize))
	b.LastBatchSize.Store(int32(batchSize))

	// Exponential moving average of latency
	latencyMs := elapsed.Milliseconds()
	oldAvg := b.AvgLatencyMs.Load()
	if oldAvg == 0 {
		b.AvgLatencyMs.Store(latencyMs)
	} else {
		// EMA with alpha=0.3
		newAvg := int64(float64(oldAvg)*0.7 + float64(latencyMs)*0.3)
		b.AvgLatencyMs.Store(newAvg)
	}

	log.Printf("📦 Batch executed: size=%d, latency=%v", batchSize, elapsed)

	// Distribute results
	if err != nil {
		for _, r := range batch {
			r.ErrCh <- err
		}
		return
	}

	for i, r := range batch {
		queueWait := start.Sub(r.EnqueueAt)
		resp := &pb.InferResponse{
			RequestId:    r.Req.RequestId,
			Result:       results[i],
			LatencyNs:    elapsed.Nanoseconds(),
			BatchSize:    int32(batchSize),
			QueueWaitMs:  int32(queueWait.Milliseconds()),
			PriorityUsed: r.Req.Priority.String(),
		}
		r.DoneCh <- resp
	}

	// Adaptive wait tuning
	b.adaptWait()
}

func (b *Batcher) adaptWait() {
	depth := b.queue.Depth()
	b.mu.Lock()
	defer b.mu.Unlock()

	switch {
	case depth > 100:
		// High pressure — flush faster
		b.currentWait = 20 * time.Millisecond
	case depth < 10:
		// Low pressure — wait longer for bigger batches
		b.currentWait = 80 * time.Millisecond
	default:
		// Normal
		b.currentWait = b.cfg.MaxWaitTime
	}
}

func (b *Batcher) drainRemaining() {
	for {
		batch := b.queue.DequeueN(b.cfg.MaxBatchSize)
		if len(batch) == 0 {
			return
		}
		b.executeBatch(batch)
	}
}
