package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

type traceKey struct{}

type Metrics struct {
	mu       sync.Mutex
	service  string
	started  time.Time
	requests map[string]RequestMetric
}

type RequestMetric struct {
	Method          string           `json:"method"`
	Path            string           `json:"path"`
	Count           int64            `json:"count"`
	Errors          int64            `json:"errors"`
	TotalDuration   time.Duration    `json:"total_duration"`
	DurationBuckets map[string]int64 `json:"duration_buckets,omitempty"`
	LastStatus      int              `json:"last_status"`
	LastTraceID     string           `json:"last_trace_id"`
	LastSeenAt      time.Time        `json:"last_seen_at"`
}

type RunMetric struct {
	Role            string           `json:"role"`
	Executor        string           `json:"executor"`
	Status          string           `json:"status"`
	Count           int64            `json:"count"`
	Active          int64            `json:"active"`
	TotalDuration   time.Duration    `json:"total_duration"`
	DurationBuckets map[string]int64 `json:"duration_buckets,omitempty"`
}

var requestDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
var runDurationBuckets = []float64{1, 5, 10, 30, 60, 300, 900, 1800, 3600, 7200}

func NewMetrics(service string) *Metrics {
	return &Metrics{
		service:  service,
		started:  time.Now().UTC(),
		requests: map[string]RequestMetric{},
	}
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := traceIDFromHeaders(r)
		w.Header().Set("X-Multi-Codex-Trace-Id", traceID)
		w.Header().Set("Traceparent", fmt.Sprintf("00-%s-%s-01", traceID, NewSpanID()))
		ctx := context.WithValue(r.Context(), traceKey{}, traceID)
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(recorder, r.WithContext(ctx))
		m.Record(r.Method, normalizeMetricPath(r.URL.Path), recorder.status, time.Since(start), traceID)
	})
}

func (m *Metrics) Record(method string, path string, status int, duration time.Duration, traceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := method + " " + path
	metric := m.requests[key]
	metric.Method = method
	metric.Path = path
	metric.Count++
	if status >= 400 {
		metric.Errors++
	}
	metric.TotalDuration += duration
	metric.DurationBuckets = incrementDurationBuckets(metric.DurationBuckets, requestDurationBuckets, duration)
	metric.LastStatus = status
	metric.LastTraceID = traceID
	metric.LastSeenAt = time.Now().UTC()
	m.requests[key] = metric
}

func (m *Metrics) Snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()

	requests := make([]RequestMetric, 0, len(m.requests))
	for _, metric := range m.requests {
		requests = append(requests, metric)
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].Path == requests[j].Path {
			return requests[i].Method < requests[j].Method
		}
		return requests[i].Path < requests[j].Path
	})
	return map[string]any{
		"service":  m.service,
		"started":  m.started,
		"requests": requests,
	}
}

func (m *Metrics) PrometheusText() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	requests := make([]RequestMetric, 0, len(m.requests))
	for _, metric := range m.requests {
		requests = append(requests, metric)
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].Path == requests[j].Path {
			return requests[i].Method < requests[j].Method
		}
		return requests[i].Path < requests[j].Path
	})

	var b strings.Builder
	uptime := time.Since(m.started).Seconds()
	fmt.Fprintf(&b, "# HELP multi_codex_service_uptime_seconds Service uptime in seconds.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_service_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "multi_codex_service_uptime_seconds{service=%q} %.3f\n", m.service, uptime)
	fmt.Fprintf(&b, "# HELP multi_codex_http_requests_total HTTP requests observed by service, method, path, and last status.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_http_requests_total counter\n")
	for _, metric := range requests {
		fmt.Fprintf(&b, "multi_codex_http_requests_total{service=%q,method=%q,path=%q,status=%q} %d\n", m.service, metric.Method, metric.Path, fmt.Sprint(metric.LastStatus), metric.Count)
	}
	fmt.Fprintf(&b, "# HELP multi_codex_http_request_errors_total HTTP requests with status >= 400.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_http_request_errors_total counter\n")
	for _, metric := range requests {
		fmt.Fprintf(&b, "multi_codex_http_request_errors_total{service=%q,method=%q,path=%q} %d\n", m.service, metric.Method, metric.Path, metric.Errors)
	}
	fmt.Fprintf(&b, "# HELP multi_codex_http_request_duration_seconds_total Total HTTP request duration in seconds.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_http_request_duration_seconds_total counter\n")
	for _, metric := range requests {
		fmt.Fprintf(&b, "multi_codex_http_request_duration_seconds_total{service=%q,method=%q,path=%q} %.6f\n", m.service, metric.Method, metric.Path, metric.TotalDuration.Seconds())
	}
	fmt.Fprintf(&b, "# HELP multi_codex_http_request_duration_seconds HTTP request duration histogram in seconds.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_http_request_duration_seconds histogram\n")
	for _, metric := range requests {
		for _, bucket := range requestDurationBuckets {
			label := bucketLabel(bucket)
			fmt.Fprintf(&b, "multi_codex_http_request_duration_seconds_bucket{service=%q,method=%q,path=%q,le=%q} %d\n", m.service, metric.Method, metric.Path, label, metric.DurationBuckets[label])
		}
		fmt.Fprintf(&b, "multi_codex_http_request_duration_seconds_bucket{service=%q,method=%q,path=%q,le=\"+Inf\"} %d\n", m.service, metric.Method, metric.Path, metric.Count)
		fmt.Fprintf(&b, "multi_codex_http_request_duration_seconds_sum{service=%q,method=%q,path=%q} %.6f\n", m.service, metric.Method, metric.Path, metric.TotalDuration.Seconds())
		fmt.Fprintf(&b, "multi_codex_http_request_duration_seconds_count{service=%q,method=%q,path=%q} %d\n", m.service, metric.Method, metric.Path, metric.Count)
	}
	return b.String()
}

func (m *Metrics) OTLPExport(runs []domain.Run) map[string]any {
	snapshot := m.Snapshot()
	requests, _ := snapshot["requests"].([]RequestMetric)
	now := time.Now().UTC()
	metrics := []map[string]any{
		otlpGaugeMetric(
			"multi_codex_service_uptime_seconds",
			"Service uptime in seconds.",
			"s",
			[]map[string]any{otlpNumberPoint(map[string]string{"service": m.service}, time.Since(m.started).Seconds(), now)},
		),
	}

	requestPoints := make([]map[string]any, 0, len(requests))
	errorPoints := make([]map[string]any, 0, len(requests))
	durationPoints := make([]map[string]any, 0, len(requests))
	requestDurationHistogramPoints := make([]map[string]any, 0, len(requests))
	for _, metric := range requests {
		attrs := map[string]string{
			"service": metricService(m.service),
			"method":  metric.Method,
			"path":    metric.Path,
			"status":  fmt.Sprint(metric.LastStatus),
		}
		requestPoints = append(requestPoints, otlpIntPoint(attrs, metric.Count, now))
		errorAttrs := map[string]string{
			"service": metricService(m.service),
			"method":  metric.Method,
			"path":    metric.Path,
		}
		errorPoints = append(errorPoints, otlpIntPoint(errorAttrs, metric.Errors, now))
		durationPoints = append(durationPoints, otlpNumberPoint(errorAttrs, metric.TotalDuration.Seconds(), now))
		requestDurationHistogramPoints = append(requestDurationHistogramPoints, otlpHistogramPoint(errorAttrs, metric.Count, metric.TotalDuration.Seconds(), requestDurationBuckets, metric.DurationBuckets, now))
	}
	metrics = append(metrics,
		otlpSumMetric("multi_codex_http_requests_total", "HTTP requests observed by service, method, path, and last status.", "1", requestPoints, true),
		otlpSumMetric("multi_codex_http_request_errors_total", "HTTP requests with status >= 400.", "1", errorPoints, true),
		otlpSumMetric("multi_codex_http_request_duration_seconds_total", "Total HTTP request duration in seconds.", "s", durationPoints, true),
		otlpHistogramMetric("multi_codex_http_request_duration_seconds", "HTTP request duration histogram in seconds.", "s", requestDurationHistogramPoints),
	)

	runMetrics := RunMetricsSnapshot(runs)
	runCountPoints := make([]map[string]any, 0, len(runMetrics))
	activeRunPoints := make([]map[string]any, 0, len(runMetrics))
	runDurationPoints := make([]map[string]any, 0, len(runMetrics))
	runDurationHistogramPoints := make([]map[string]any, 0, len(runMetrics))
	for _, metric := range runMetrics {
		attrs := map[string]string{
			"service":  metricService(m.service),
			"role":     metric.Role,
			"executor": metric.Executor,
			"status":   metric.Status,
		}
		runCountPoints = append(runCountPoints, otlpIntPoint(attrs, metric.Count, now))
		activeRunPoints = append(activeRunPoints, otlpIntPoint(attrs, metric.Active, now))
		runDurationPoints = append(runDurationPoints, otlpNumberPoint(attrs, metric.TotalDuration.Seconds(), now))
		runDurationHistogramPoints = append(runDurationHistogramPoints, otlpHistogramPoint(attrs, completedRunCount(metric.DurationBuckets), metric.TotalDuration.Seconds(), runDurationBuckets, metric.DurationBuckets, now))
	}
	metrics = append(metrics,
		otlpSumMetric("multi_codex_runs_total", "Runs observed by role, executor, and status.", "1", runCountPoints, true),
		otlpGaugeMetric("multi_codex_active_runs", "Active runs by role, executor, and status.", "1", activeRunPoints),
		otlpSumMetric("multi_codex_run_duration_seconds_total", "Total completed run duration by role, executor, and status.", "s", runDurationPoints, true),
		otlpHistogramMetric("multi_codex_run_duration_seconds", "Completed run duration histogram in seconds.", "s", runDurationHistogramPoints),
	)

	return map[string]any{
		"resourceMetrics": []map[string]any{
			{
				"resource": map[string]any{
					"attributes": otlpAttributes(map[string]string{"service.name": m.service}),
				},
				"scopeMetrics": []map[string]any{
					{
						"scope": map[string]any{
							"name":    "multi-codex/internal/observability",
							"version": "dev",
						},
						"metrics": metrics,
					},
				},
			},
		},
	}
}

func WantsPrometheus(r *http.Request) bool {
	if r.URL.Query().Get("format") == "prometheus" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/plain") || strings.Contains(accept, "application/openmetrics-text")
}

func WantsOTLP(r *http.Request) bool {
	if r.URL.Query().Get("format") == "otlp" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/x-otlp-json")
}

func RunMetricsSnapshot(runs []domain.Run) []RunMetric {
	metricsByKey := map[string]RunMetric{}
	for _, run := range runs {
		role := run.Role
		if role == "" {
			role = "unknown"
		}
		executor := run.Executor
		if executor == "" {
			executor = "unknown"
		}
		status := run.Status
		if status == "" {
			status = "unknown"
		}
		key := role + "\x00" + executor + "\x00" + status
		metric := metricsByKey[key]
		metric.Role = role
		metric.Executor = executor
		metric.Status = status
		metric.Count++
		if runIsActive(status) {
			metric.Active++
		}
		if run.StartedAt != nil && run.FinishedAt != nil && run.FinishedAt.After(*run.StartedAt) {
			duration := run.FinishedAt.Sub(*run.StartedAt)
			metric.TotalDuration += duration
			metric.DurationBuckets = incrementDurationBuckets(metric.DurationBuckets, runDurationBuckets, duration)
		}
		metricsByKey[key] = metric
	}
	metrics := make([]RunMetric, 0, len(metricsByKey))
	for _, metric := range metricsByKey {
		metrics = append(metrics, metric)
	}
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].Role != metrics[j].Role {
			return metrics[i].Role < metrics[j].Role
		}
		if metrics[i].Executor != metrics[j].Executor {
			return metrics[i].Executor < metrics[j].Executor
		}
		return metrics[i].Status < metrics[j].Status
	})
	return metrics
}

func RunsPrometheusText(service string, runs []domain.Run) string {
	metrics := RunMetricsSnapshot(runs)
	var b strings.Builder
	fmt.Fprintf(&b, "# HELP multi_codex_runs_total Runs observed by role, executor, and status.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_runs_total counter\n")
	for _, metric := range metrics {
		fmt.Fprintf(&b, "multi_codex_runs_total{service=%q,role=%q,executor=%q,status=%q} %d\n", service, metric.Role, metric.Executor, metric.Status, metric.Count)
	}
	fmt.Fprintf(&b, "# HELP multi_codex_active_runs Active runs by role, executor, and status.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_active_runs gauge\n")
	for _, metric := range metrics {
		fmt.Fprintf(&b, "multi_codex_active_runs{service=%q,role=%q,executor=%q,status=%q} %d\n", service, metric.Role, metric.Executor, metric.Status, metric.Active)
	}
	fmt.Fprintf(&b, "# HELP multi_codex_run_duration_seconds_total Total completed run duration by role, executor, and status.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_run_duration_seconds_total counter\n")
	for _, metric := range metrics {
		fmt.Fprintf(&b, "multi_codex_run_duration_seconds_total{service=%q,role=%q,executor=%q,status=%q} %.6f\n", service, metric.Role, metric.Executor, metric.Status, metric.TotalDuration.Seconds())
	}
	fmt.Fprintf(&b, "# HELP multi_codex_run_duration_seconds Completed run duration histogram in seconds.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_run_duration_seconds histogram\n")
	for _, metric := range metrics {
		completed := completedRunCount(metric.DurationBuckets)
		for _, bucket := range runDurationBuckets {
			label := bucketLabel(bucket)
			fmt.Fprintf(&b, "multi_codex_run_duration_seconds_bucket{service=%q,role=%q,executor=%q,status=%q,le=%q} %d\n", service, metric.Role, metric.Executor, metric.Status, label, metric.DurationBuckets[label])
		}
		fmt.Fprintf(&b, "multi_codex_run_duration_seconds_bucket{service=%q,role=%q,executor=%q,status=%q,le=\"+Inf\"} %d\n", service, metric.Role, metric.Executor, metric.Status, completed)
		fmt.Fprintf(&b, "multi_codex_run_duration_seconds_sum{service=%q,role=%q,executor=%q,status=%q} %.6f\n", service, metric.Role, metric.Executor, metric.Status, metric.TotalDuration.Seconds())
		fmt.Fprintf(&b, "multi_codex_run_duration_seconds_count{service=%q,role=%q,executor=%q,status=%q} %d\n", service, metric.Role, metric.Executor, metric.Status, completed)
	}
	return b.String()
}

func runIsActive(status string) bool {
	switch status {
	case "queued", "preparing", "running":
		return true
	default:
		return false
	}
}

func incrementDurationBuckets(existing map[string]int64, bounds []float64, duration time.Duration) map[string]int64 {
	if existing == nil {
		existing = map[string]int64{}
	}
	seconds := duration.Seconds()
	for _, bound := range bounds {
		if seconds <= bound {
			existing[bucketLabel(bound)]++
		}
	}
	existing["+Inf"]++
	return existing
}

func bucketLabel(value float64) string {
	return strconvFormatFloat(value)
}

func completedRunCount(buckets map[string]int64) int64 {
	if buckets == nil {
		return 0
	}
	return buckets["+Inf"]
}

func TraceID(ctx context.Context) string {
	value, _ := ctx.Value(traceKey{}).(string)
	return value
}

func NewTraceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

func NewSpanID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("15040500")))[:16]
	}
	return hex.EncodeToString(b[:])
}

func traceIDFromHeaders(r *http.Request) string {
	if traceID := traceIDFromTraceparent(r.Header.Get("Traceparent")); traceID != "" {
		return traceID
	}
	if traceID := r.Header.Get("X-Request-Id"); validTraceID(traceID) {
		return strings.ToLower(traceID)
	}
	return NewTraceID()
}

func traceIDFromTraceparent(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) < 4 {
		return ""
	}
	if len(parts[0]) != 2 || !hexOnly(parts[0]) {
		return ""
	}
	traceID := strings.ToLower(parts[1])
	if !validTraceID(traceID) {
		return ""
	}
	spanID := strings.ToLower(parts[2])
	if len(spanID) != 16 || !hexOnly(spanID) || allZero(spanID) {
		return ""
	}
	if len(parts[3]) != 2 || !hexOnly(parts[3]) {
		return ""
	}
	return traceID
}

func validTraceID(value string) bool {
	return len(value) == 32 && hexOnly(value) && !allZero(value)
}

func hexOnly(value string) bool {
	for _, r := range value {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' {
			continue
		}
		return false
	}
	return true
}

func allZero(value string) bool {
	for _, r := range value {
		if r != '0' {
			return false
		}
	}
	return true
}

func otlpSumMetric(name string, description string, unit string, points []map[string]any, monotonic bool) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"unit":        unit,
		"sum": map[string]any{
			"aggregationTemporality": "AGGREGATION_TEMPORALITY_CUMULATIVE",
			"isMonotonic":            monotonic,
			"dataPoints":             points,
		},
	}
}

func otlpGaugeMetric(name string, description string, unit string, points []map[string]any) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"unit":        unit,
		"gauge": map[string]any{
			"dataPoints": points,
		},
	}
}

func otlpHistogramMetric(name string, description string, unit string, points []map[string]any) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"unit":        unit,
		"histogram": map[string]any{
			"aggregationTemporality": "AGGREGATION_TEMPORALITY_CUMULATIVE",
			"dataPoints":             points,
		},
	}
}

func otlpIntPoint(attrs map[string]string, value int64, at time.Time) map[string]any {
	return map[string]any{
		"attributes":   otlpAttributes(attrs),
		"timeUnixNano": fmt.Sprint(at.UnixNano()),
		"asInt":        fmt.Sprint(value),
	}
}

func otlpNumberPoint(attrs map[string]string, value float64, at time.Time) map[string]any {
	return map[string]any{
		"attributes":   otlpAttributes(attrs),
		"timeUnixNano": fmt.Sprint(at.UnixNano()),
		"asDouble":     value,
	}
}

func otlpHistogramPoint(attrs map[string]string, count int64, sum float64, bounds []float64, cumulative map[string]int64, at time.Time) map[string]any {
	bucketCounts := make([]string, 0, len(bounds)+1)
	var previous int64
	for _, bound := range bounds {
		current := cumulative[bucketLabel(bound)]
		bucketCounts = append(bucketCounts, fmt.Sprint(current-previous))
		previous = current
	}
	bucketCounts = append(bucketCounts, fmt.Sprint(count-previous))
	return map[string]any{
		"attributes":     otlpAttributes(attrs),
		"timeUnixNano":   fmt.Sprint(at.UnixNano()),
		"count":          fmt.Sprint(count),
		"sum":            sum,
		"bucketCounts":   bucketCounts,
		"explicitBounds": bounds,
	}
}

func otlpAttributes(attrs map[string]string) []map[string]any {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		values = append(values, map[string]any{
			"key": key,
			"value": map[string]any{
				"stringValue": attrs[key],
			},
		})
	}
	return values
}

func metricService(service string) string {
	if service == "" {
		return "unknown"
	}
	return service
}

func strconvFormatFloat(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", value), "0"), ".")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func normalizeMetricPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if isDynamicSegment(part) {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}

func isDynamicSegment(value string) bool {
	if len(value) >= 32 && strings.Contains(value, "-") {
		return true
	}
	if strings.HasPrefix(value, "019") && len(value) >= 16 {
		return true
	}
	return false
}
