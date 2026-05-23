package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"dns-forwarder/internal/cache"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

type Server struct {
	http  *http.Server
	cache *cache.Cache
	log   *zap.Logger
}

func NewServer(addr string, c *cache.Cache, log *zap.Logger) *Server {
	mux := http.NewServeMux()
	s := &Server{
		cache: c,
		log:   log,
		http:  &http.Server{Addr: addr, Handler: mux},
	}
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/health", handleHealth)
	return s
}

func (s *Server) Start() error {
	s.log.Info("metrics listening", zap.String("addr", s.http.Addr))
	go func() {
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("metrics server error", zap.Error(err))
		}
	}()
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// handleStats returns a JSON snapshot of runtime counters.
func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	stats := s.cache.Stats()

	// Sync gauge so Prometheus scrape and /stats are consistent.
	CacheSize.Set(float64(stats.Size))

	var hitRate float64
	total := stats.Hits + stats.Misses
	if total > 0 {
		hitRate = float64(stats.Hits) / float64(total)
	}

	payload := map[string]any{
		"cache": map[string]any{
			"size":     stats.Size,
			"hits":     stats.Hits,
			"misses":   stats.Misses,
			"hit_rate": fmt.Sprintf("%.2f%%", hitRate*100),
		},
		"time": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload)
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(w, `{"status":"ok"}`)
}
