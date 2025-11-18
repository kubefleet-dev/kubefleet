package main

import (
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// Get cluster and workload information from environment variables
	clusterName := os.Getenv("CLUSTER_NAME")
	if clusterName == "" {
		clusterName = "unknown"
	}
	
	workloadName := os.Getenv("WORKLOAD_NAME")
	if workloadName == "" {
		workloadName = "unknown"
	}

	// Define a simple gauge metric for health with labels
	workloadHealth := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "workload_health",
			Help: "Indicates if the workload is healthy (1=healthy, 0=unhealthy)",
		},
		[]string{"cluster_name", "workload_name"},
	)

	// Set it to 1 (healthy) with labels
	workloadHealth.WithLabelValues(clusterName, workloadName).Set(1)

	// Register metric with Prometheus default registry
	prometheus.MustRegister(workloadHealth)

	// Expose metrics endpoint
	http.Handle("/metrics", promhttp.Handler())

	// Start HTTP server
	http.ListenAndServe(":8080", nil)
}
