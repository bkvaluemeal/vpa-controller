package controller

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"
	"time"

	"vpa-controller/pkg/prometheus"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	vpav1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

type VPAReconciler struct {
	client.Client
	Log              logr.Logger
	Scheme           *runtime.Scheme
	Recorder         record.EventRecorder
	PrometheusClient   prometheus.Client
	AnnotationPrefix   string
	DefaultRangeVector string
}

func (r *VPAReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("vpa", req.NamespacedName)

	vpa := &vpav1.VerticalPodAutoscaler{}
	if err := r.Get(ctx, req.NamespacedName, vpa); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	scheduleStr, ok := vpa.Annotations[r.AnnotationPrefix+"/schedule"]
	if !ok {
		// Not managed by us
		return ctrl.Result{}, nil
	}

	schedule, err := time.ParseDuration(scheduleStr)
	if err != nil {
		r.Recorder.Event(vpa, "Warning", "InvalidSchedule", fmt.Sprintf("Invalid schedule format: %v", err))
		VPASyncErrorCount.WithLabelValues(vpa.Namespace, vpa.Name, "InvalidSchedule").Inc()
		return ctrl.Result{}, nil
	}

	lastSyncStr, hasLastSync := vpa.Annotations[r.AnnotationPrefix+"/last-sync"]
	if hasLastSync {
		lastSync, err := time.Parse(time.RFC3339, lastSyncStr)
		if err == nil {
			if time.Since(lastSync) < schedule {
				return ctrl.Result{RequeueAfter: schedule - time.Since(lastSync)}, nil
			}
		}
	}

	// Start reconciliation
	log.Info("Starting VPA synchronization")

	if vpa.Spec.ResourcePolicy == nil {
		vpa.Spec.ResourcePolicy = &vpav1.PodResourcePolicy{}
	}

	// Identify containers from annotations
	containerQueries := make(map[string]map[string]map[string]string) // container -> resource -> min/max -> query
	containerOOMQueries := make(map[string]string)
	containerRVFormulas := make(map[string]string)

	for k, v := range vpa.Annotations {
		if !strings.HasPrefix(k, r.AnnotationPrefix+"/") {
			continue
		}

		key := strings.TrimPrefix(k, r.AnnotationPrefix+"/")

		if strings.HasSuffix(key, "-query-oom-count") {
			containerName := strings.TrimSuffix(key, "-query-oom-count")
			containerOOMQueries[containerName] = v
			continue
		}

		if strings.HasSuffix(key, "-range-vector-formula") {
			containerName := strings.TrimSuffix(key, "-range-vector-formula")
			containerRVFormulas[containerName] = v
			continue
		}

		if strings.Contains(key, "query") {
			parts := strings.Split(key, "-")
			if len(parts) < 4 {
				continue
			}
			// Prefix/Container-query-Resource-Bound
			containerName := strings.Join(parts[0:len(parts)-3], "-")
			resourceType := parts[len(parts)-2] // cpu or memory
			bound := parts[len(parts)-1]       // min or max

			if containerQueries[containerName] == nil {
				containerQueries[containerName] = make(map[string]map[string]string)
			}
			if containerQueries[containerName][resourceType] == nil {
				containerQueries[containerName][resourceType] = make(map[string]string)
			}
			containerQueries[containerName][resourceType][bound] = v
		}
	}

	if len(containerQueries) == 0 {
		return ctrl.Result{RequeueAfter: schedule}, nil
	}

	for containerName, resources := range containerQueries {
		var policy *vpav1.ContainerResourcePolicy
		for i := range vpa.Spec.ResourcePolicy.ContainerPolicies {
			if vpa.Spec.ResourcePolicy.ContainerPolicies[i].ContainerName == containerName {
				policy = &vpa.Spec.ResourcePolicy.ContainerPolicies[i]
				break
			}
		}

		if policy == nil {
			vpa.Spec.ResourcePolicy.ContainerPolicies = append(vpa.Spec.ResourcePolicy.ContainerPolicies, vpav1.ContainerResourcePolicy{
				ContainerName: containerName,
			})
			policy = &vpa.Spec.ResourcePolicy.ContainerPolicies[len(vpa.Spec.ResourcePolicy.ContainerPolicies)-1]
		}

		// Calculate Range Vector for this container
		var oomCount int64
		if oomQuery, ok := containerOOMQueries[containerName]; ok {
			// Ensure the OOM query has a range vector to return multiple samples (matrix)
			finalOOMQuery := oomQuery
			if !strings.Contains(oomQuery, "[") {
				finalOOMQuery = fmt.Sprintf("%s[%s]", oomQuery, r.DefaultRangeVector)
			}

			var err error
			log.Info("Executing OOM count query", "container", containerName, "query", finalOOMQuery)
			oomCount, err = r.PrometheusClient.Query(ctx, finalOOMQuery)
			if err != nil {
				if err == prometheus.ErrNoResults {
					log.Info("OOM count query returned no results, defaulting to 0", "container", containerName, "query", finalOOMQuery)
				} else {
					log.Error(err, "Failed to query OOM count, defaulting to 0", "container", containerName, "query", finalOOMQuery)
					r.Recorder.Event(vpa, "Warning", "OOMQueryError", fmt.Sprintf("Error querying OOM count for %s: %v", containerName, err))
				}
				oomCount = 0
			}
		}

		rangeVector := r.DefaultRangeVector
		if formula, ok := containerRVFormulas[containerName]; ok {
			tmpl, err := template.New("rv").Funcs(GetTemplateFuncMap()).Parse(formula)
			if err != nil {
				log.Error(err, "Failed to parse range vector formula, defaulting to "+r.DefaultRangeVector, "container", containerName)
				r.Recorder.Event(vpa, "Warning", "FormulaParseError", fmt.Sprintf("Error parsing range vector formula for %s: %v", containerName, err))
			} else {
				var buf bytes.Buffer
				data := struct {
					Count int64
				}{
					Count: oomCount,
				}
				if err := tmpl.Execute(&buf, data); err != nil {
					log.Error(err, "Failed to execute range vector formula, defaulting to "+r.DefaultRangeVector, "container", containerName)
					r.Recorder.Event(vpa, "Warning", "FormulaExecuteError", fmt.Sprintf("Error executing range vector formula for %s: %v", containerName, err))
				} else {
					rangeVector = buf.String()
				}
			}
		}

		templateData := struct {
			Count       int64
			RangeVector string
		}{
			Count:       oomCount,
			RangeVector: rangeVector,
		}

		for resourceType, bounds := range resources {
			var resName corev1.ResourceName
			switch resourceType {
			case "cpu":
				resName = corev1.ResourceCPU
			case "memory":
				resName = corev1.ResourceMemory
			default:
				continue
			}

			for bound, queryTmpl := range bounds {
				tmpl, err := template.New("query").Funcs(GetTemplateFuncMap()).Parse(queryTmpl)
				var query string
				if err != nil {
					// If it's not a valid template, maybe it's just a raw query
					query = queryTmpl
				} else {
					var buf bytes.Buffer
					if err := tmpl.Execute(&buf, templateData); err != nil {
						log.Error(err, "Failed to execute query template, using raw query", "container", containerName, "resource", resourceType, "bound", bound)
						query = queryTmpl
					} else {
						query = buf.String()
					}
				}

				log.Info("Executing resource query", "container", containerName, "resource", resourceType, "bound", bound, "query", query)
				val, err := r.PrometheusClient.Query(ctx, query)
				if err != nil {
					if err == prometheus.ErrNoResults {
						log.Info("Resource query returned no results, skipping", "container", containerName, "resource", resourceType, "bound", bound, "query", query)
					} else {
						r.Recorder.Event(vpa, "Warning", "QueryError", fmt.Sprintf("Error querying for %s/%s/%s: %v", containerName, resourceType, bound, err))
						VPASyncErrorCount.WithLabelValues(vpa.Namespace, vpa.Name, "QueryError").Inc()
					}
					continue
				}

				var q resource.Quantity
				if resourceType == "cpu" {
					q = *resource.NewMilliQuantity(val, resource.DecimalSI)
				} else {
					q = *resource.NewQuantity(val, resource.BinarySI)
				}

				if bound == "min" {
					if policy.MinAllowed == nil {
						policy.MinAllowed = make(corev1.ResourceList)
					}
					existing, exists := policy.MinAllowed[resName]
					if !exists || !existing.Equal(q) {
						policy.MinAllowed[resName] = q
					}
				} else if bound == "max" {
					if policy.MaxAllowed == nil {
						policy.MaxAllowed = make(corev1.ResourceList)
					}
					existing, exists := policy.MaxAllowed[resName]
					if !exists || !existing.Equal(q) {
						policy.MaxAllowed[resName] = q
					}
				}
			}
		}
	}

	// Update last-sync annotation anyway if we attempted sync
	if vpa.Annotations == nil {
		vpa.Annotations = make(map[string]string)
	}
	vpa.Annotations[r.AnnotationPrefix+"/last-sync"] = time.Now().Format(time.RFC3339)

	if err := r.Update(ctx, vpa); err != nil {
		if errors.IsConflict(err) {
			// Optimistic Concurrency Control failure, just requeue
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "Failed to update VPA")
		VPASyncErrorCount.WithLabelValues(vpa.Namespace, vpa.Name, "UpdateError").Inc()
		return ctrl.Result{}, err
	}

	log.Info("Successfully synchronized VPA")
	r.Recorder.Event(vpa, "Normal", "Synced", "VPA successfully synchronized with Prometheus values")
	VPASyncSuccessCount.WithLabelValues(vpa.Namespace, vpa.Name).Inc()

	return ctrl.Result{RequeueAfter: schedule}, nil
}

func (r *VPAReconciler) SetupWithManager(mgr ctrl.Manager, workers int) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vpav1.VerticalPodAutoscaler{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: workers}).
		Complete(r)
}
