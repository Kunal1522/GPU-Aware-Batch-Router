package router

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Broadcaster pushes cluster state to connected dashboard clients via WebSocket.
type Broadcaster struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]bool
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		clients: make(map[*websocket.Conn]bool),
	}
}

// HandleWS is the WebSocket upgrade handler for /ws.
func (b *Broadcaster) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("⚠️  WebSocket upgrade failed: %v", err)
		return
	}

	b.mu.Lock()
	b.clients[conn] = true
	b.mu.Unlock()

	log.Printf("📊 Dashboard client connected (%d total)", len(b.clients))

	// Read loop (to detect disconnect)
	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.clients, conn)
			b.mu.Unlock()
			conn.Close()
			log.Printf("📊 Dashboard client disconnected (%d remain)", len(b.clients))
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

// ClusterState is the JSON payload pushed to the dashboard.
type ClusterState struct {
	Workers             []WorkerState    `json:"workers"`
	RoutingDistribution map[string]int64 `json:"routing_distribution"`
	TotalRequests       int64            `json:"total_requests"`
}

type WorkerState struct {
	ID             string  `json:"id"`
	Address        string  `json:"address"`
	Score          float64 `json:"score"`
	VRAMFreeGB     float64 `json:"vram_free_gb"`
	VRAMTotalGB    float64 `json:"vram_total_gb"`
	GPUUtilization float64 `json:"gpu_utilization"`
	TemperatureC   float64 `json:"temperature_c"`
	QueueDepth     int32   `json:"queue_depth"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	CurrentBatch   int32   `json:"current_batch"`
	Healthy        bool    `json:"healthy"`
}

// Broadcast sends the cluster state to all connected WebSocket clients.
func (b *Broadcaster) Broadcast(state *ClusterState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for conn := range b.clients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			conn.Close()
			delete(b.clients, conn)
		}
	}
}
