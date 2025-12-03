package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// Define a simple gauge metric for health with labels
	workloadHealth := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "workload_health",
			Help: "Indicates if the workload is healthy (1=healthy, 0=unhealthy)",
		},
	)

	// Set it to 1 (healthy) with labels
	workloadHealth.Set(1)

	// Register metric with Prometheus default registry
	prometheus.MustRegister(workloadHealth)

	// Expose metrics endpoint
	http.Handle("/metrics", promhttp.Handler())

	// Start HTTP server
	http.ListenAndServe(":8080", nil)
}
