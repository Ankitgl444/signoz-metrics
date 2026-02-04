# signoz-metrics
Repository showcasing OpenTelemetry SDK to instrument metrics

A tiny Go HTTP server instrumented with **OpenTelemetry Metrics** and configured to export metrics to **SigNoz** via **OTLP**.

It emits three metric types:
- **Counter**: error requests (5xx)
- **Histogram**: request latency (ms)
- **Gauge**: current number of items in an in-memory cart

---

## What this repo contains

- `main.go` — runnable HTTP server with OpenTelemetry metrics instrumentation
- `go.mod` / `go.sum` — Go module dependencies

---

## Prerequisites

- Go installed: 1.21 or later
- A SigNoz Cloud workspace (or a self-hosted SigNoz/Collector endpoint)
- SigNoz **Ingestion Key** + **OTLP endpoint** (for SigNoz Cloud)

