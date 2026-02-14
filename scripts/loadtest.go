package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/kunal/gpu-batch-router/gen/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "Router address")
	concurrency := flag.Int("concurrency", 50, "Number of concurrent clients")
	duration := flag.Duration("duration", 30*time.Second, "Test duration")
	flag.Parse()

	log.Printf("🚀 Load test starting: addr=%s, concurrency=%d, duration=%v", *addr, *concurrency, *duration)

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewInferenceServiceClient(conn)

	var (
		totalRequests atomic.Int64
		totalErrors   atomic.Int64
		mu            sync.Mutex
		latencies     []time.Duration
		workerDist    = make(map[string]int)
		priorityDist  = make(map[string]int)
	)

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	start := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Pick random priority
				priorities := []pb.Priority{pb.Priority_LOW, pb.Priority_MEDIUM, pb.Priority_HIGH}
				weights := []int{60, 30, 10} // 60% LOW, 30% MEDIUM, 10% HIGH
				r := rand.Intn(100)
				var pri pb.Priority
				if r < weights[0] {
					pri = priorities[0]
				} else if r < weights[0]+weights[1] {
					pri = priorities[1]
				} else {
					pri = priorities[2]
				}

				reqStart := time.Now()
				resp, err := client.Infer(ctx, &pb.InferRequest{
					RequestId: fmt.Sprintf("req-%d-%d", clientID, totalRequests.Load()),
					Payload:   make([]byte, 1024), // 1KB payload
					Timestamp: time.Now().UnixNano(),
					ModelName: "resnet50",
					Priority:  pri,
				})

				if err != nil {
					totalErrors.Add(1)
					continue
				}

				elapsed := time.Since(reqStart)
				totalRequests.Add(1)

				mu.Lock()
				latencies = append(latencies, elapsed)
				workerDist[resp.WorkerId]++
				priorityDist[resp.PriorityUsed]++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Calculate percentiles
	mu.Lock()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	mu.Unlock()

	total := totalRequests.Load()
	errors := totalErrors.Load()
	throughput := float64(total) / elapsed.Seconds()

	fmt.Println("\n" + "═══════════════════════════════════════════════════")
	fmt.Println("   🏁 LOAD TEST RESULTS")
	fmt.Println("═══════════════════════════════════════════════════")
	fmt.Printf("   Duration:      %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("   Concurrency:   %d\n", *concurrency)
	fmt.Printf("   Total Reqs:    %d\n", total)
	fmt.Printf("   Errors:        %d (%.1f%%)\n", errors, float64(errors)/float64(total+errors)*100)
	fmt.Printf("   Throughput:    %.1f req/sec\n", throughput)
	fmt.Println()

	if len(latencies) > 0 {
		fmt.Println("   📊 Latency Percentiles:")
		fmt.Printf("      p50:  %v\n", latencies[len(latencies)*50/100])
		fmt.Printf("      p95:  %v\n", latencies[len(latencies)*95/100])
		fmt.Printf("      p99:  %v\n", latencies[len(latencies)*99/100])
		fmt.Printf("      max:  %v\n", latencies[len(latencies)-1])
	}

	fmt.Println()
	fmt.Println("   🎯 Routing Distribution:")
	for worker, count := range workerDist {
		pct := float64(count) / float64(total) * 100
		fmt.Printf("      %s: %d (%.1f%%)\n", worker, count, pct)
	}

	fmt.Println()
	fmt.Println("   🏷️  Priority Distribution:")
	for pri, count := range priorityDist {
		pct := float64(count) / float64(total) * 100
		fmt.Printf("      %s: %d (%.1f%%)\n", pri, count, pct)
	}
	fmt.Println("═══════════════════════════════════════════════════")
}
