package adapter

import (
	"context"
	"time"

	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	helper "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/events"

	"github.com/fluxcd/helm-controller/internal/controller"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type HelmReleaseAdapter struct {
	ClientOpts        runtimeClient.Options
	KubeConfigOpts    runtimeClient.KubeConfigOptions
	ReconcilerOptions HelmReleaseReconcilerOption
	ControllerName    string
	MetricOptions     helper.Metrics
	LeaderElection    *bool
}

type HelmReleaseReconcilerOption struct {
	HTTPRetry                 int
	DependencyRequeueInterval time.Duration
	RateLimiter               workqueue.TypedRateLimiter[reconcile.Request]
}

func SetupHelmReconciler(ctx context.Context, mgr ctrl.Manager, adapter *HelmReleaseAdapter) error {
	var eventRecorder *events.Recorder
	var err error

	if eventRecorder, err = events.NewRecorder(mgr, mgr.GetLogger(), "", adapter.ControllerName); err != nil {
		return err
	}

	hr := &controller.HelmReleaseReconciler{
		Client:           mgr.GetClient(),
		EventRecorder:    eventRecorder,
		Metrics:          adapter.MetricOptions,
		GetClusterConfig: ctrl.GetConfig,
		ClientOpts:       adapter.ClientOpts,
		KubeConfigOpts:   adapter.KubeConfigOpts,
		LeaderElection:   adapter.LeaderElection,
		FieldManager:     adapter.ControllerName,
	}
	return hr.SetupWithManager(ctx, mgr, controller.HelmReleaseReconcilerOptions{
		DependencyRequeueInterval: adapter.ReconcilerOptions.DependencyRequeueInterval,
		HTTPRetry:                 adapter.ReconcilerOptions.HTTPRetry,
		RateLimiter:               adapter.ReconcilerOptions.RateLimiter,
	})
}
