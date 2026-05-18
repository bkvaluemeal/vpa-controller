package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"vpa-controller/pkg/prometheus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	vpav1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrl "sigs.k8s.io/controller-runtime"
)

type MockPrometheusClient struct {
	mock.Mock
}

func (m *MockPrometheusClient) Query(ctx context.Context, query string) (int64, error) {
	args := m.Called(ctx, query)
	return args.Get(0).(int64), args.Error(1)
}

func TestReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = vpav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	prefix := "vpa.prometheus.io"
	vpa := &vpav1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-vpa",
			Namespace: "default",
			Annotations: map[string]string{
				prefix + "/schedule":             "1m",
				prefix + "/app-query-cpu-min":    "cpu_query_min",
				prefix + "/app-query-memory-max": "mem_query_max",
			},
		},
		Spec: vpav1.VerticalPodAutoscalerSpec{},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vpa).Build()
	mockProm := new(MockPrometheusClient)
	mockProm.On("Query", mock.Anything, "cpu_query_min").Return(int64(100), nil)
	mockProm.On("Query", mock.Anything, "mem_query_max").Return(int64(1024*1024), nil)

	reconciler := &VPAReconciler{
		Client:             fakeClient,
		Log:                ctrl.Log.WithName("test"),
		Scheme:             scheme,
		Recorder:           record.NewFakeRecorder(10),
		PrometheusClient:   mockProm,
		AnnotationPrefix:   prefix,
		DefaultRangeVector: "1h",
	}

	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(vpa)}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	updatedVPA := &vpav1.VerticalPodAutoscaler{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedVPA)
	assert.NoError(t, err)

	assert.NotNil(t, updatedVPA.Spec.ResourcePolicy)
	assert.Len(t, updatedVPA.Spec.ResourcePolicy.ContainerPolicies, 1)
	policy := updatedVPA.Spec.ResourcePolicy.ContainerPolicies[0]
	assert.Equal(t, "app", policy.ContainerName)

	assert.Equal(t, resource.NewMilliQuantity(100, resource.DecimalSI).MilliValue(), policy.MinAllowed.Cpu().MilliValue())
	assert.Equal(t, resource.NewQuantity(1024*1024, resource.BinarySI).Value(), policy.MaxAllowed.Memory().Value())

	assert.Contains(t, updatedVPA.Annotations, prefix+"/last-sync")
}

func TestReconcileWithOOMAndFormula(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = vpav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	prefix := "vpa.prometheus.io"
	vpa := &vpav1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oom-vpa",
			Namespace: "default",
			Annotations: map[string]string{
				prefix + "/schedule":                      "1m",
				prefix + "/app-query-oom-count":           "oom_query",
				prefix + "/app-range-vector-formula":      "{{mul .Count 5}}m",
				prefix + "/app-query-cpu-min":             "avg(rate(cpu[{{.RangeVector}}]))",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vpa).Build()
	mockProm := new(MockPrometheusClient)
	// OOM count is 2. Note: it appends [1h] if not present because DefaultRangeVector is set to 1h
	mockProm.On("Query", mock.Anything, "oom_query[1h]").Return(int64(2), nil)
	// RangeVector should be 2*5 = 10m
	mockProm.On("Query", mock.Anything, "avg(rate(cpu[10m]))").Return(int64(500), nil)

	reconciler := &VPAReconciler{
		Client:             fakeClient,
		Log:                ctrl.Log.WithName("test"),
		Scheme:             scheme,
		Recorder:           record.NewFakeRecorder(10),
		PrometheusClient:   mockProm,
		AnnotationPrefix:   prefix,
		DefaultRangeVector: "1h",
	}

	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(vpa)}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	updatedVPA := &vpav1.VerticalPodAutoscaler{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedVPA)
	assert.NoError(t, err)

	policy := updatedVPA.Spec.ResourcePolicy.ContainerPolicies[0]
	assert.Equal(t, resource.NewMilliQuantity(500, resource.DecimalSI).MilliValue(), policy.MinAllowed.Cpu().MilliValue())
	mockProm.AssertExpectations(t)
}

func TestReconcileHyphenatedContainer(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = vpav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	prefix := "vpa.prometheus.io"
	vpa := &vpav1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hyphen-vpa",
			Namespace: "default",
			Annotations: map[string]string{
				prefix + "/schedule":                  "1m",
				prefix + "/istio-proxy-query-cpu-min": "istio_cpu_query",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vpa).Build()
	mockProm := new(MockPrometheusClient)
	mockProm.On("Query", mock.Anything, "istio_cpu_query").Return(int64(150), nil)

	reconciler := &VPAReconciler{
		Client:             fakeClient,
		Log:                ctrl.Log.WithName("test"),
		Scheme:             scheme,
		Recorder:           record.NewFakeRecorder(10),
		PrometheusClient:   mockProm,
		AnnotationPrefix:   prefix,
		DefaultRangeVector: "1h",
	}

	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(vpa)}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	updatedVPA := &vpav1.VerticalPodAutoscaler{}
	err = fakeClient.Get(context.Background(), req.NamespacedName, updatedVPA)
	assert.NoError(t, err)

	assert.Len(t, updatedVPA.Spec.ResourcePolicy.ContainerPolicies, 1)
	policy := updatedVPA.Spec.ResourcePolicy.ContainerPolicies[0]
	assert.Equal(t, "istio-proxy", policy.ContainerName)
	assert.Equal(t, resource.NewMilliQuantity(150, resource.DecimalSI).MilliValue(), policy.MinAllowed.Cpu().MilliValue())

	mockProm.AssertExpectations(t)
}

func TestReconcileOOMQueryRangeDefaulting(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = vpav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	prefix := "vpa.prometheus.io"
	vpa := &vpav1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oom-range-vpa",
			Namespace: "default",
			Annotations: map[string]string{
				prefix + "/schedule":                 "1m",
				prefix + "/app-query-oom-count":      "oom_metric", // No range vector
				prefix + "/app-range-vector-formula": "5m",
				prefix + "/app-query-cpu-min":        "cpu_query",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vpa).Build()
	mockProm := new(MockPrometheusClient)
	// Expect oom_metric[1h] because DefaultRangeVector is 1h and query has no [
	mockProm.On("Query", mock.Anything, "oom_metric[1h]").Return(int64(3), nil)
	mockProm.On("Query", mock.Anything, "cpu_query").Return(int64(100), nil)

	reconciler := &VPAReconciler{
		Client:             fakeClient,
		Log:                ctrl.Log.WithName("test"),
		Scheme:             scheme,
		Recorder:           record.NewFakeRecorder(10),
		PrometheusClient:   mockProm,
		AnnotationPrefix:   prefix,
		DefaultRangeVector: "1h",
	}

	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(vpa)}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	mockProm.AssertExpectations(t)
}

func TestReconcileOOMQueryErrorFallback(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = vpav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	prefix := "vpa.prometheus.io"
	vpa := &vpav1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oom-error-vpa",
			Namespace: "default",
			Annotations: map[string]string{
				prefix + "/schedule":                      "1m",
				prefix + "/app-query-oom-count":           "oom_query_fail",
				prefix + "/app-range-vector-formula":      "{{if gt .Count 0}}10m{{else}}5m{{end}}",
				prefix + "/app-query-cpu-min":             "cpu_query[{{.RangeVector}}]",
			},
		},
	}

	recorder := record.NewFakeRecorder(10)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vpa).Build()
	mockProm := new(MockPrometheusClient)
	mockProm.On("Query", mock.Anything, "oom_query_fail[1h]").Return(int64(0), fmt.Errorf("prom error"))
	// Should fallback to oomCount=0, so RangeVector="5m"
	mockProm.On("Query", mock.Anything, "cpu_query[5m]").Return(int64(200), nil)

	reconciler := &VPAReconciler{
		Client:             fakeClient,
		Log:                ctrl.Log.WithName("test"),
		Scheme:             scheme,
		Recorder:           recorder,
		PrometheusClient:   mockProm,
		AnnotationPrefix:   prefix,
		DefaultRangeVector: "1h", // doesn't matter much here since formula provides it
	}

	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(vpa)}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	mockProm.AssertExpectations(t)

	// Verify event was emitted for real error
	found := false
	for {
		select {
		case event := <-recorder.Events:
			if strings.Contains(event, "OOMQueryError") {
				found = true
			}
		default:
			goto end
		}
	}
end:
	assert.True(t, found, "Expected OOMQueryError event")
}

func TestReconcileNoResultsGraceful(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = vpav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	prefix := "vpa.prometheus.io"
	vpa := &vpav1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-results-vpa",
			Namespace: "default",
			Annotations: map[string]string{
				prefix + "/schedule":                 "1m",
				prefix + "/app-query-oom-count":      "oom_no_results",
				prefix + "/app-range-vector-formula": "5m",
				prefix + "/app-query-cpu-min":        "cpu_no_results",
			},
		},
	}

	recorder := record.NewFakeRecorder(10)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vpa).Build()
	mockProm := new(MockPrometheusClient)
	mockProm.On("Query", mock.Anything, "oom_no_results[1h]").Return(int64(0), prometheus.ErrNoResults)
	mockProm.On("Query", mock.Anything, "cpu_no_results").Return(int64(0), prometheus.ErrNoResults)

	reconciler := &VPAReconciler{
		Client:             fakeClient,
		Log:                ctrl.Log.WithName("test"),
		Scheme:             scheme,
		Recorder:           recorder,
		PrometheusClient:   mockProm,
		AnnotationPrefix:   prefix,
		DefaultRangeVector: "1h",
	}

	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(vpa)}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	mockProm.AssertExpectations(t)

	// Verify NO events were emitted
	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, "Synced") { // Synced is normal
			t.Errorf("Unexpected event emitted: %s", event)
		}
	default:
	}
}

func TestReconcileDefaultRangeVector(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = vpav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	prefix := "vpa.prometheus.io"
	vpa := &vpav1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-rv-vpa",
			Namespace: "default",
			Annotations: map[string]string{
				prefix + "/schedule":          "1m",
				prefix + "/app-query-cpu-min": "cpu_query[{{.RangeVector}}]",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vpa).Build()
	mockProm := new(MockPrometheusClient)
	// RangeVector should be the configured default "5m"
	mockProm.On("Query", mock.Anything, "cpu_query[5m]").Return(int64(300), nil)

	reconciler := &VPAReconciler{
		Client:             fakeClient,
		Log:                ctrl.Log.WithName("test"),
		Scheme:             scheme,
		Recorder:           record.NewFakeRecorder(10),
		PrometheusClient:   mockProm,
		AnnotationPrefix:   prefix,
		DefaultRangeVector: "5m",
	}

	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(vpa)}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	mockProm.AssertExpectations(t)
}
