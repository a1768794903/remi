package metrics

import (
	"crypto/subtle"
	"net/http"
	"os"
	"runtime"
	"strconv"
)

// Handler exposes the same authentication boundary as the Python metrics
// endpoint. The Go process metrics are intentionally low-cardinality and safe
// to scrape; request-specific labels are never emitted here.
type Handler struct{}

func (Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("METRICS_SECRET")
	token := r.Header.Get("Authorization")
	if len(token) >= 7 && token[:7] == "Bearer " {
		token = token[7:]
	}
	if secret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte("# HELP go_goroutines Number of goroutines.\n# TYPE go_goroutines gauge\ngo_goroutines " + strconv.Itoa(runtime.NumGoroutine()) + "\n" +
		"# HELP go_memstats_heap_alloc_bytes Bytes allocated on the heap.\n# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes " + strconv.FormatUint(stats.HeapAlloc, 10) + "\n"))
}
