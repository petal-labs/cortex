// Package observability provides Prometheus metrics and structured logging for Cortex.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for Cortex.
type Metrics struct {
	// Operations metrics
	OperationsTotal   *prometheus.CounterVec
	OperationDuration *prometheus.HistogramVec

	// Search metrics
	SearchLatency     *prometheus.HistogramVec
	SearchResultCount *prometheus.HistogramVec

	// Embedding metrics
	EmbeddingLatency      *prometheus.HistogramVec
	EmbeddingRequestTotal *prometheus.CounterVec

	// Entity extraction metrics
	ExtractionQueueSize       prometheus.GaugeFunc
	ExtractionDeadLetterCount prometheus.GaugeFunc
	ExtractionProcessedTotal  *prometheus.CounterVec

	// Storage metrics
	StorageOperationDuration *prometheus.HistogramVec
}

// QueueStatsProvider is an interface for getting extraction queue statistics.
type QueueStatsProvider interface {
	GetQueueSize() int64
	GetDeadLetterCount() int64
}

// NewMetrics creates and registers all Prometheus metrics for Cortex.
// If queueStats is nil, queue metrics will report 0.
func NewMetrics(queueStats QueueStatsProvider) *Metrics {
	m := &Metrics{
		// cortex_operations_total - Total operations by primitive and action
		OperationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cortex_operations_total",
				Help: "Total number of operations by primitive and action",
			},
			[]string{"primitive", "action", "namespace", "status"},
		),

		// cortex_operation_duration_seconds - Operation latency
		OperationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "cortex_operation_duration_seconds",
				Help:    "Duration of operations in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"primitive", "action"},
		),

		// cortex_search_latency_seconds - Vector search latency
		SearchLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "cortex_search_latency_seconds",
				Help:    "Vector search latency in seconds",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
			},
			[]string{"primitive", "namespace"},
		),

		// cortex_search_result_count - Number of search results returned
		SearchResultCount: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "cortex_search_result_count",
				Help:    "Number of search results returned",
				Buckets: []float64{0, 1, 5, 10, 20, 50, 100},
			},
			[]string{"primitive"},
		),

		// cortex_embedding_latency_seconds - Iris embedding call latency
		EmbeddingLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "cortex_embedding_latency_seconds",
				Help:    "Embedding API call latency in seconds",
				Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
			},
			[]string{"provider", "model"},
		),

		// cortex_embedding_requests_total - Total embedding API calls
		EmbeddingRequestTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cortex_embedding_requests_total",
				Help: "Total number of embedding API requests",
			},
			[]string{"provider", "model", "status"},
		),

		// cortex_extraction_processed_total - Extraction items processed
		ExtractionProcessedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cortex_extraction_processed_total",
				Help: "Total number of extraction items processed",
			},
			[]string{"status"},
		),

		// cortex_storage_operation_duration_seconds - Storage backend latency
		StorageOperationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "cortex_storage_operation_duration_seconds",
				Help:    "Storage operation latency in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"backend", "operation"},
		),
	}

	// Register gauge funcs for queue metrics
	if queueStats != nil {
		m.ExtractionQueueSize = promauto.NewGaugeFunc(
			prometheus.GaugeOpts{
				Name: "cortex_extraction_queue_size",
				Help: "Current number of pending items in the extraction queue",
			},
			func() float64 { return float64(queueStats.GetQueueSize()) },
		)

		m.ExtractionDeadLetterCount = promauto.NewGaugeFunc(
			prometheus.GaugeOpts{
				Name: "cortex_extraction_dead_letter_count",
				Help: "Number of failed extraction items in dead letter queue",
			},
			func() float64 { return float64(queueStats.GetDeadLetterCount()) },
		)
	}

	return m
}

// RecordOperation records an operation metric.
func (m *Metrics) RecordOperation(primitive, action, namespace, status string, durationSeconds float64) {
	m.OperationsTotal.WithLabelValues(primitive, action, namespace, status).Inc()
	m.OperationDuration.WithLabelValues(primitive, action).Observe(durationSeconds)
}

// RecordSearch records a search operation metric.
func (m *Metrics) RecordSearch(primitive, namespace string, durationSeconds float64, resultCount int) {
	m.SearchLatency.WithLabelValues(primitive, namespace).Observe(durationSeconds)
	m.SearchResultCount.WithLabelValues(primitive).Observe(float64(resultCount))
}

// RecordEmbedding records an embedding API call metric.
func (m *Metrics) RecordEmbedding(provider, model, status string, durationSeconds float64) {
	m.EmbeddingLatency.WithLabelValues(provider, model).Observe(durationSeconds)
	m.EmbeddingRequestTotal.WithLabelValues(provider, model, status).Inc()
}

// RecordExtraction records an extraction processing metric.
func (m *Metrics) RecordExtraction(status string) {
	m.ExtractionProcessedTotal.WithLabelValues(status).Inc()
}

// RecordStorageOperation records a storage backend operation metric.
func (m *Metrics) RecordStorageOperation(backend, operation string, durationSeconds float64) {
	m.StorageOperationDuration.WithLabelValues(backend, operation).Observe(durationSeconds)
}

// DefaultMetrics is the global metrics instance used when metrics are enabled.
var DefaultMetrics *Metrics

// InitMetrics initializes the default metrics instance.
func InitMetrics(queueStats QueueStatsProvider) {
	DefaultMetrics = NewMetrics(queueStats)
}
