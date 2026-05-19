package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	VPASyncSuccessCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vpa_sync_success_total",
			Help: "Total number of successful VPA synchronizations",
		},
		[]string{"namespace", "name"},
	)
	VPASyncErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vpa_sync_error_total",
			Help: "Total number of failed VPA synchronizations",
		},
		[]string{"namespace", "name", "reason"},
	)
)

func init() {
	metrics.Registry.MustRegister(VPASyncSuccessCount, VPASyncErrorCount)
}
