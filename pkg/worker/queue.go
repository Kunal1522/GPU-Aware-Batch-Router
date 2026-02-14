package worker

import (
	"container/heap"
	"sync"
	"time"

	pb "github.com/kunal/gpu-batch-router/gen/inference/v1"
)

// PendingRequest wraps a gRPC request with channels for the response.
type PendingRequest struct {
	Req       *pb.InferRequest
	DoneCh    chan *pb.InferResponse
	ErrCh     chan error
	EnqueueAt time.Time
	index     int // used by heap
}

// PriorityQueue implements heap.Interface for PendingRequests.
// HIGH priority requests are dequeued first. Within the same priority, FIFO.
type PriorityQueue struct {
	mu    sync.Mutex
	items []*PendingRequest
}

func NewPriorityQueue() *PriorityQueue {
	pq := &PriorityQueue{
		items: make([]*PendingRequest, 0, 64),
	}
	heap.Init(pq)
	return pq
}

// Push adds a request to the priority queue (thread-safe).
func (pq *PriorityQueue) Enqueue(req *PendingRequest) {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	heap.Push(pq, req)
}

// DequeueN removes up to n highest-priority requests (thread-safe).
func (pq *PriorityQueue) DequeueN(n int) []*PendingRequest {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	if len(pq.items) == 0 {
		return nil
	}
	count := n
	if count > len(pq.items) {
		count = len(pq.items)
	}
	result := make([]*PendingRequest, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, heap.Pop(pq).(*PendingRequest))
	}
	return result
}

// Len returns current queue depth (thread-safe).
func (pq *PriorityQueue) Depth() int {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	return len(pq.items)
}

// --- heap.Interface implementation (not thread-safe, use Enqueue/DequeueN) ---

func (pq *PriorityQueue) Len() int { return len(pq.items) }

func (pq *PriorityQueue) Less(i, j int) bool {
	// Higher priority number = dequeued first
	if pq.items[i].Req.Priority != pq.items[j].Req.Priority {
		return pq.items[i].Req.Priority > pq.items[j].Req.Priority
	}
	// Same priority: earlier timestamp first (FIFO)
	return pq.items[i].Req.Timestamp < pq.items[j].Req.Timestamp
}

func (pq *PriorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
	pq.items[i].index = i
	pq.items[j].index = j
}

func (pq *PriorityQueue) Push(x interface{}) {
	item := x.(*PendingRequest)
	item.index = len(pq.items)
	pq.items = append(pq.items, item)
}

func (pq *PriorityQueue) Pop() interface{} {
	old := pq.items
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	pq.items = old[:n-1]
	return item
}
