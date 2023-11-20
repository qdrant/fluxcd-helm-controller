package adapter

import (
	"context"
	"time"

	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	helper "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/events"
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
	MetricOptions       helper.Metrics
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

	pollingOpts := polling.Options{}
	hr := &controller.HelmReleaseReconciler{
		Client:              mgr.GetClient(),
		Config:              mgr.GetConfig(),
		Scheme:              mgr.GetScheme(),
		EventRecorder:       eventRecorder,
		Metrics:             adapter.MetricOptions,
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
