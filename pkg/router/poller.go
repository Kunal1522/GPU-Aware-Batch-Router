package router

import (
	"context"
	"log"
	"sync"
	"time"
)

// Poller periodically fetches metrics from all registered workers.
type Poller struct {
	registry *Registry
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

func NewPoller(registry *Registry, interval time.Duration) *Poller {
	return &Poller{
		registry: registry,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the polling loop.
func (p *Poller) Start() {
	p.wg.Add(1)
	go p.loop()
	log.Printf("📡 Poller started: interval=%v", p.interval)
}

// Stop gracefully shuts down the poller.
func (p *Poller) Stop() {
	close(p.stopCh)
	p.wg.Wait()
}

func (p *Poller) loop() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Do an immediate first poll
	p.pollAll()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.pollAll()
		}
	}
}

func (p *Poller) pollAll() {
	workers := p.registry.GetAll()
	var wg sync.WaitGroup

	for _, w := range workers {
		if w.MetricsClient == nil {
			continue
		}
		wg.Add(1)
		go func(entry *WorkerEntry) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			metrics, err := entry.MetricsClient.GetMetrics(ctx, nil)
			if err != nil {
				p.registry.MarkFailed(entry.Address)
				return
			}

			p.registry.UpdateMetrics(entry.Address, metrics)
		}(w)
	}

	wg.Wait()
}
