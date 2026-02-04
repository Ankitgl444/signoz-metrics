package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

func initMeterProvider(ctx context.Context) (func(context.Context) error, error) {

	// exporter automatically reads OTEL_EXPORTER_OTLP_METRICS_ENDPOINT + HEADERS from env.
	exporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("new otlp metric grpc exporter failed: %w", err)
	}

	// Resource reads OTEL_RESOURCE_ATTRIBUTES (service.name=...) from env.
	res, err := resource.New(
		ctx,
		resource.WithFromEnv(),
		resource.WithHost(),
		resource.WithProcess(),
		resource.WithOS(),
	)
	if err != nil {
		return nil, fmt.Errorf("new resource failed: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(10*time.Second))),
	)

	otel.SetMeterProvider(mp)
	return mp.Shutdown, nil
}

type respWriter struct {
	http.ResponseWriter
	status int
}

func (rw *respWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func main() {
	ctx := context.Background()
	shutdown, err := initMeterProvider(ctx)
	if err != nil {
		log.Fatalf("init meter provider: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	m := otel.Meter("assignment-metrics")

	// --- Assignment metrics ---
	// Counter: number of error requests (5xx)
	errorRequests, _ := m.Int64Counter(
		"http.error_requests",
		metric.WithDescription("Count of HTTP 5xx responses"),
	)

	// Histogram: request latency
	requestLatencyMs, _ := m.Float64Histogram(
		"http.duration_ms",
		metric.WithUnit("ms"),
		metric.WithDescription("HTTP request latency in milliseconds"),
	)

	// Gauge: number of items in cart
	var cartItems int64
	_, _ = m.Int64ObservableGauge(
		"cart.items",
		metric.WithDescription("Current number of items in cart"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			o.Observe(atomic.LoadInt64(&cartItems))
			return nil
		}),
	)

	withMetrics := func(route string, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &respWriter{ResponseWriter: w, status: 200}

			next(rw, r)

			latMs := float64(time.Since(start).Milliseconds())

			attrs := metric.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", route),
				attribute.Int("http.status_code", rw.status),
			)

			requestLatencyMs.Record(r.Context(), latMs, attrs)

			if rw.status >= 500 {
				errorRequests.Add(r.Context(), 1, attrs)
			}
		}
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/ok", withMetrics("/ok", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(20+rand.Intn(150)) * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	mux.HandleFunc("/error", withMetrics("/error", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(30+rand.Intn(200)) * time.Millisecond)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))

	mux.HandleFunc("/cart/add", withMetrics("/cart/add", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("count"))
		if n <= 0 {
			n = 1
		}
		atomic.AddInt64(&cartItems, int64(n))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf("cartItems=%d", atomic.LoadInt64(&cartItems))))
	}))

	mux.HandleFunc("/cart/items", withMetrics("/cart/items", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf("%d", atomic.LoadInt64(&cartItems))))
	}))

	srv := &http.Server{Addr: ":8080", Handler: mux}

	// Graceful shutdown
	go func() {
		log.Println("listening on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctxShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctxShutdown)
	log.Println("shutdown complete")
}
