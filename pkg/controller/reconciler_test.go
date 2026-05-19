package controller

import (
	"context"
	"testing"

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
				prefix + "/schedule":                  "1m",
				prefix + "/app/query/cpu/min":         "cpu_query_min",
				prefix + "/app/query/memory/max":      "mem_query_max",
			},
		},
		Spec: vpav1.VerticalPodAutoscalerSpec{},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vpa).Build()
	mockProm := new(MockPrometheusClient)
	mockProm.On("Query", mock.Anything, "cpu_query_min").Return(int64(100), nil)
	mockProm.On("Query", mock.Anything, "mem_query_max").Return(int64(1024*1024), nil)

	reconciler := &VPAReconciler{
		Client:           fakeClient,
		Log:              ctrl.Log.WithName("test"),
		Scheme:           scheme,
		Recorder:         record.NewFakeRecorder(10),
		PrometheusClient: mockProm,
		AnnotationPrefix: prefix,
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
