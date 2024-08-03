package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/go-logr/logr"
	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/samber/lo"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	backsnapv1alpha1 "github.com/skybitsnl/backsnap/api/v1alpha1"
	"github.com/skybitsnl/backsnap/internal/controller"
	//+kubebuilder:scaffold:imports
)

// nolint: lll
var (
	metricsAddr          = flag.String("metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	probeAddr            = flag.String("health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	enableLeaderElection = flag.Bool("leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	namespacesFlag        = flag.String("namespaces", "", "limit to namespaces, comma-separated (default is all namespaces)")
	excludeNamespacesFlag = flag.String("exclude-namespaces", "", "exclude namespaces")
	defaultSchedule       = flag.String("schedule", "@daily", "Default backup schedule, can be overridden per namespace or per PVC with annotations - set to empty if you want no automatic backups")
	manual                = flag.Bool("manual", false, "Manual mode: don't automatically create any PVCBackup objects")
	maxRunningBackups     = flag.Int("max-running-backups", 1, "Maximum amount of backups to run simultaneously")
	sleepBetweenBackups   = flag.Int("sleep-between-backups", 30, "Seconds to sleep between backing up of each PVC")

	snapshotClassFlag = flag.String("snapshotclass", "", "volumeSnapshotClassName")
	volumeClassFlag   = flag.String("volumeclass", "", "volumeClassName")
	imagePullSecret   = flag.String("imagepullsecret", "", "imagePullSecret to pass to backup Pod (optional)")
	image             = flag.String("image", "sjorsgielen/backsnap-restic:latest-main", "Restic back-up image")
	s3Host            = flag.String("s3-host", "", "S3 hostname (can be host, host:port or http://host:port/)")
	s3Bucket          = flag.String("s3-bucket", "", "S3 bucket")
	s3AccessKeyId     = flag.String("s3-access-key-id", "", "S3 access key ID")
	s3SecretAccessKey = flag.String("s3-secret-access-key", "", "S3 secret access key")
	resticPassword    = flag.String("restic-password", "", "Restic password to encrypt storage by")
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(batchv1.AddToScheme(scheme))
	utilruntime.Must(volumesnapshotv1.AddToScheme(scheme))

	utilruntime.Must(backsnapv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func requiredFlag(fn string) {
	f := flag.Lookup(fn)
	if f.Value.String() == "" {
		log.Fatal("Flag -" + f.Name + " is required")
	}
}

func main() {
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{})))

	ctx := logr.NewContextWithSlogLogger(context.Background(), slog.Default())
	setupLog := logr.FromContextOrDiscard(ctx)
	ctrl.SetLogger(setupLog)

	requiredFlag("image")
	requiredFlag("s3-host")
	requiredFlag("s3-bucket")

	namespaces := lo.Map(strings.Split(*namespacesFlag, ","), ignore1[string, int](strings.TrimSpace))
	excludeNamespaces := lo.Map(strings.Split(*excludeNamespacesFlag, ","), ignore1[string, int](strings.TrimSpace))

	if len(namespaces) > 1 {
		// filter out "" as it would imply all namespaces when there's also particular namespaces mentioned
		// if this filters out all of them, we'll add 'all namespaces' right after this
		namespaces = lo.Filter(namespaces, func(item string, _ int) bool { return item != "" })
	}

	if len(namespaces) == 0 {
		namespaces = append(namespaces, "")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: *metricsAddr},
		HealthProbeBindAddress: *probeAddr,
		LeaderElection:         *enableLeaderElection,
		LeaderElectionID:       "leader.backsnap.skyb.it",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		LeaderElectionReleaseOnCancel: true,

		// TODO:
		// Note that the Manager can restrict the namespace that all controllers
		// will watch for resources by
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if !*manual {
		if err := (&controller.AutomaticPVCBackupCreator{
			Client:            mgr.GetClient(),
			Scheme:            mgr.GetScheme(),
			Namespaces:        namespaces,
			ExcludeNamespaces: excludeNamespaces,
			DefaultSchedule:   *defaultSchedule,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to setup AutomaticPVCBackupCreator")
			os.Exit(1)
		}
	}

	backupSettings := controller.BackupSettings{
		SnapshotClass:     *snapshotClassFlag,
		VolumeClass:       *volumeClassFlag,
		ImagePullSecret:   *imagePullSecret,
		Image:             *image,
		S3Host:            *s3Host,
		S3Bucket:          *s3Bucket,
		S3AccessKeyId:     *s3AccessKeyId,
		S3SecretAccessKey: *s3SecretAccessKey,
		ResticPassword:    *resticPassword,
	}

	if err = (&controller.PVCBackupReconciler{
		Client:              mgr.GetClient(),
		Scheme:              mgr.GetScheme(),
		Namespaces:          namespaces,
		ExcludeNamespaces:   excludeNamespaces,
		MaxRunningBackups:   *maxRunningBackups,
		SleepBetweenBackups: *sleepBetweenBackups,
		BackupSettings:      backupSettings,
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PVCBackup")
		os.Exit(1)
	}
	if err = (&controller.PVCRestoreReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		Namespaces:        namespaces,
		ExcludeNamespaces: excludeNamespaces,
		BackupSettings:    backupSettings,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PVCRestore")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	slog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func ignore1[T any, U any, V any](f func(t T) V) func(t T, u U) V {
	return func(t T, u U) V {
		return f(t)
	}
}
