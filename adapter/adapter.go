package adapter

import (
	"context"
	"time"

	v2 "github.com/fluxcd/helm-controller/api/v2beta1"
	helper "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/runtime/metrics"

	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	"sigs.k8s.io/controller-runtime/pkg/ratelimiter"

	"github.com/fluxcd/helm-controller/internal/controller"
	ctrl "sigs.k8s.io/controller-runtime"
)

type HelmReleaseAdapter struct {
	NoCrossNamespaceRef bool
	ClientOpts          runtimeClient.Options
	KubeConfigOpts      runtimeClient.KubeConfigOptions
	ReconcilerOptions   HelmReleaseReconcilerOption
	ControllerName      string
}

type HelmReleaseReconcilerOption struct {
	HTTPRetry                 int
	DependencyRequeueInterval time.Duration
	RateLimiter               ratelimiter.RateLimiter
}

func SetupHelmReconciler(ctx context.Context, mgr ctrl.Manager, adapter *HelmReleaseAdapter) error {
	var eventRecorder *events.Recorder
	var err error

	if eventRecorder, err = events.NewRecorder(mgr, mgr.GetLogger(), "", adapter.ControllerName); err != nil {
		return err
	}

	metricsH := helper.NewMetrics(mgr, metrics.MustMakeRecorder(), v2.HelmReleaseFinalizer)

	pollingOpts := polling.Options{}
	hr := &controller.HelmReleaseReconciler{
		Client:              mgr.GetClient(),
		Config:              mgr.GetConfig(),
		Scheme:              mgr.GetScheme(),
		EventRecorder:       eventRecorder,
		Metrics:             metricsH,
		NoCrossNamespaceRef: adapter.NoCrossNamespaceRef,
		ClientOpts:          adapter.ClientOpts,
		KubeConfigOpts:      adapter.KubeConfigOpts,
		PollingOpts:         pollingOpts,
		StatusPoller:        polling.NewStatusPoller(mgr.GetClient(), mgr.GetRESTMapper(), pollingOpts),
		ControllerName:      adapter.ControllerName,
	}
	return hr.SetupWithManager(ctx, mgr, controller.HelmReleaseReconcilerOptions{
		DependencyRequeueInterval: adapter.ReconcilerOptions.DependencyRequeueInterval,
		HTTPRetry:                 adapter.ReconcilerOptions.HTTPRetry,
		RateLimiter:               adapter.ReconcilerOptions.RateLimiter,
	})
}
