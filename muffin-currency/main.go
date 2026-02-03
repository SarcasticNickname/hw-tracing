package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/openzipkin/zipkin-go"
	zipkinhttp "github.com/openzipkin/zipkin-go/reporter/http"
	"github.com/openzipkin/zipkin-go/model"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type CurrencyRate struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	Rate float64 `json:"rate"`
}

var tracer *zipkin.Tracer

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "muffin_currency_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "muffin_currency_request_duration_seconds",
			Help:    "HTTP request duration",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	currencyRateGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "muffin_currency_rate",
			Help: "Current currency exchange rate",
		},
		[]string{"from", "to"},
	)

	healthStatus = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "muffin_currency_health",
			Help: "Health status (1 = healthy)",
		},
	)
)

var rates = map[string]map[string]float64{
	"CARAMEL":   {"CHOKOLATE": 0.85, "PLAIN": 75.50, "CARAMEL": 1},
	"CHOKOLATE": {"CARAMEL": 1.18, "PLAIN": 89.00, "CHOKOLATE": 1},
	"PLAIN":     {"CHOKOLATE": 0.013, "CARAMEL": 0.011, "PLAIN": 1},
}

func initTracing(serviceName, zipkinURL, port string) error {
	reporter := zipkinhttp.NewReporter(zipkinURL)

	host, _ := os.Hostname()
	endpoint, err := zipkin.NewEndpoint(serviceName, host+":"+port)
	if err != nil {
		return fmt.Errorf("failed to create zipkin endpoint: %w", err)
	}

	t, err := zipkin.NewTracer(
		reporter,
		zipkin.WithLocalEndpoint(endpoint),
		zipkin.WithTraceID128Bit(true),
		zipkin.WithSharedSpans(false),
		zipkin.WithSampler(zipkin.AlwaysSample),
	)
	if err != nil {
		return fmt.Errorf("failed to create zipkin tracer: %w", err)
	}

	tracer = t

	slog.Info("Zipkin tracing initialized",
		"service", serviceName,
		"zipkin_url", zipkinURL,
		"local_endpoint", host+":"+port,
	)

	return nil
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rw := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()

		httpRequestsTotal.WithLabelValues(
			r.Method,
			r.URL.Path,
			fmt.Sprintf("%d", rw.statusCode),
		).Inc()

		httpRequestDuration.WithLabelValues(
			r.Method,
			r.URL.Path,
		).Observe(duration)
	})
}

func tracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parentCtx, err := ParseTraceparent(r.Header.Get("traceparent"))
		if err != nil {
			slog.Warn("Failed to parse traceparent", "error", err, "traceparent", r.Header.Get("traceparent"))
		}

		if tracer == nil {
			next.ServeHTTP(w, r)
			return
		}

		isZeroTrace := parentCtx.TraceID.High == 0 && parentCtx.TraceID.Low == 0

		var span zipkin.Span
		if isZeroTrace {
			span = tracer.StartSpan(r.URL.Path, zipkin.Kind(model.Server))
		} else {
			span = tracer.StartSpan(r.URL.Path, zipkin.Kind(model.Server), zipkin.Parent(parentCtx))
		}
		defer span.Finish()

		ctx := zipkin.NewContext(r.Context(), span)

		span.Tag("http.method", r.Method)
		span.Tag("http.url", r.URL.String())
		span.Tag("http.path", r.URL.Path)
		span.Tag("component", "http")

		traceID := span.Context().TraceID.String()
		w.Header().Set("X-Trace-ID", traceID)

		rw := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		slog.Info("Request started",
			"method", r.Method,
			"path", r.URL.Path,
			"trace_id", traceID,
			"span_id", span.Context().ID.String(),
		)

		next.ServeHTTP(rw, r.WithContext(ctx))

		span.Tag("http.status_code", fmt.Sprintf("%d", rw.statusCode))
		if rw.statusCode >= 400 {
			span.Tag("error", "true")
		}

		slog.Info("Request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"trace_id", traceID,
		)
	})
}

func getRateHandler(w http.ResponseWriter, r *http.Request) {
	span := zipkin.SpanFromContext(r.Context())
	traceID := ""
	if span != nil {
		traceID = span.Context().TraceID.String()
		span.Tag("handler", "get_rate")
		span.Tag("operation", "currency_conversion")
	}

	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	slog.Info("Processing currency conversion",
		"from", from,
		"to", to,
		"trace_id", traceID,
	)

	if from == "" || to == "" {
		if span != nil {
			span.Tag("error", "missing_parameters")
		}
		http.Error(w, `{"error":"Missing 'from' or 'to' parameter"}`, http.StatusBadRequest)
		return
	}

	rate, exists := rates[from][to]
	if !exists {
		if span != nil {
			span.Tag("error", "pair_not_found")
		}
		http.Error(w, `{"error":"Currency pair not found"}`, http.StatusNotFound)
		return
	}

	if span != nil {
		span.Tag("currency.from", from)
		span.Tag("currency.to", to)
		span.Tag("currency.rate", fmt.Sprintf("%f", rate))
	}

	currencyRateGauge.WithLabelValues(from, to).Set(rate)

	resp := CurrencyRate{From: from, To: to, Rate: rate}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)

	slog.Info("Currency rate returned",
		"from", from,
		"to", to,
		"rate", rate,
		"trace_id", traceID,
	)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	span := zipkin.SpanFromContext(r.Context())
	traceID := ""
	if span != nil {
		traceID = span.Context().TraceID.String()
		span.Tag("handler", "health_check")
	}

	healthStatus.Set(1)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"trace_id":  traceID,
	})
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	span := zipkin.SpanFromContext(r.Context())
	traceID := ""
	if span != nil {
		traceID = span.Context().TraceID.String()
		span.Tag("handler", "readiness_check")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":    "ready",
		"timestamp": time.Now().Format(time.RFC3339),
		"trace_id":  traceID,
	})
}

func main() {
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		logDir = "/logs"
	}

	_ = os.MkdirAll(logDir, 0755)
	logPath := logDir + "/app.log"

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(handler))
		slog.Warn("Failed to open log file, fallback to stdout only", "error", err, "log_path", logPath)
	} else {
		defer logFile.Close()
		mw := io.MultiWriter(os.Stdout, logFile)
		handler := slog.NewJSONHandler(mw, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(handler))
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	zipkinEndpoint := os.Getenv("ZIPKIN_ENDPOINT")
	if zipkinEndpoint == "" {
		zipkinEndpoint = "http://tempo.observability.svc.cluster.local:9411/api/v2/spans"
	}

	if err := initTracing("muffin-currency", zipkinEndpoint, port); err != nil {
		slog.Error("Failed to initialize Zipkin tracing", "error", err)
		os.Exit(1)
	}

	healthStatus.Set(1)

	mux := http.NewServeMux()
	mux.HandleFunc("/rate", getRateHandler)
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/readyz", readyHandler)
	mux.Handle("/metrics", promhttp.Handler())

	handler := metricsMiddleware(mux)
	handler = tracingMiddleware(handler)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		slog.Info("Shutting down server...")
		healthStatus.Set(0)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			slog.Error("Server forced to shutdown", "error", err)
		}
	}()

	slog.Info("Starting server", "port", port, "zipkin_endpoint", zipkinEndpoint)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("Server failed to start", "error", err)
		os.Exit(1)
	}
}

func ParseTraceparent(tp string) (model.SpanContext, error) {
	if tp == "" {
		return model.SpanContext{}, nil
	}

	parts := strings.Split(tp, "-")
	if len(parts) < 4 {
		return model.SpanContext{}, fmt.Errorf("invalid traceparent format: %s", tp)
	}

	tIDBytes, err := hex.DecodeString(parts[1])
	if err != nil || len(tIDBytes) != 16 {
		return model.SpanContext{}, fmt.Errorf("invalid traceid: %s, error: %v", parts[1], err)
	}

	traceID := model.TraceID{
		High: binary.BigEndian.Uint64(tIDBytes[:8]),
		Low:  binary.BigEndian.Uint64(tIDBytes[8:]),
	}

	sIDBytes, err := hex.DecodeString(parts[2])
	if err != nil || len(sIDBytes) != 8 {
		return model.SpanContext{}, fmt.Errorf("invalid spanid: %s, error: %v", parts[2], err)
	}

	spanID := model.ID(binary.BigEndian.Uint64(sIDBytes))

	flagsBytes, err := hex.DecodeString(parts[3])
	sampled := false
	if err == nil && len(flagsBytes) > 0 {
		sampled = (flagsBytes[0] & 0x01) == 0x01
	}

	return model.SpanContext{
		TraceID: traceID,
		ID:      spanID,
		Sampled: &sampled,
	}, nil
}
