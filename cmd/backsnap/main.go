package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"strings"
	"time"

	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/samber/lo"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	cruntimeconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	backupName            = flag.String("backupname", weekday(), "name of backup (default is current week day)")
	namespacesFlag        = flag.String("namespaces", "", "limit to namespaces, comma-separated (default is all namespaces)")
	excludeNamespacesFlag = flag.String("exclude-namespaces", "", "exclude namespaces")

	snapshotClassFlag = flag.String("snapshotclass", "", "volumeSnapshotClassName")
	volumeClassFlag   = flag.String("volumeclass", "", "volumeClassName")
	imagePullSecret   = flag.String("imagepullsecret", "", "imagePullSecret to pass to backup Pod (optional)")
	image             = flag.String("image", "sjorsgielen/backsnap-restic:latest-main", "Restic back-up image")
	s3Host            = flag.String("s3-host", "", "S3 hostname")
	s3Bucket          = flag.String("s3-bucket", "", "S3 bucket")
	s3AccessKeyId     = flag.String("s3-access-key-id", "", "S3 access key ID")
	s3SecretAccessKey = flag.String("s3-secret-access-key", "", "S3 secret access key")
	resticPassword    = flag.String("restic-password", "", "Restic password to encrypt storage by")

	sleepBetweenBackups = flag.Int("sleep-between-backups", 30, "Seconds to sleep between backing up of each PVC")
)

var (
	NoBackupAnnotation = "skyb.it/backsnap-no-backup"
)

/*
	Global TODOs:
	- allow a different schedule per PVC
	- allow different restic settings per PVC
	- https://github.com/kubernetes/client-go/blob/v0.29.0/examples/workqueue/main.go
	- write some tests for k8s interaction
	  - https://github.com/kubernetes/client-go/blob/v0.29.0/examples/fake-client/main_test.go
	  - https://github.com/kubernetes/client-go/issues/632
*/

func requiredFlag(fn string) {
	f := flag.Lookup(fn)
	if f.Value.String() == "" {
		log.Fatal("Flag -" + f.Name + " is required")
	}
}

func main() {
	flag.Parse()

	requiredFlag("image")
	requiredFlag("s3-host")
	requiredFlag("s3-bucket")

	namespaces := lo.Map(strings.Split(*namespacesFlag, ","), ignore1[string, int, string](strings.TrimSpace))
	excludeNamespaces := lo.SliceToMap(strings.Split(*excludeNamespacesFlag, ","), func(n string) (string, struct{}) {
		return strings.TrimSpace(n), struct{}{}
	})

	if len(namespaces) > 1 {
		// filter out "" as it would imply all namespaces when there's also particular namespaces mentioned
		// if this filters out all of them, we'll add 'all namespaces' right after this
		namespaces = lo.Filter(namespaces, func(item string, _ int) bool { return item != "" })
	}

	if len(namespaces) == 0 {
		namespaces = append(namespaces, "")
	}

	ctx := context.Background()

	config := cruntimeconfig.GetConfigOrDie()
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	batchv1.AddToScheme(scheme)
	volumesnapshotv1.AddToScheme(scheme)
	kclient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatal(err)
	}

	pvcs, err := SelectPVCsForBackup(ctx, kclient, namespaces, excludeNamespaces)
	if err != nil {
		log.Fatal(err)
	}

	var errs []error
	namespacesSeen := map[string]struct{}{}

	clientset := kubernetes.NewForConfigOrDie(config)
	dynamicClient := dynamic.NewForConfigOrDie(config)

	for i, pvc := range pvcs {
		namespace := pvc.Namespace
		namespacesSeen[namespace] = struct{}{}
		name := pvc.Name

		if i > 0 {
			// Wait for a bit until starting the next PVC backup, to reduce load
			// on the storage layer.
			time.Sleep(time.Duration(*sleepBetweenBackups) * time.Second)
		}

		err := BackupPvc(ctx, clientset, kclient, dynamicClient, namespace, name)
		errs = append(errs, err)
		if err != nil {
			slog.ErrorContext(ctx, "backup of PVC failed",
				slog.String("namespace", namespace),
				slog.String("pvc", name),
				slog.Any("err", err),
			)
		}
	}

	slog.InfoContext(ctx, "backup completed",
		slog.Int("pvcs", len(pvcs)),
		slog.Int("namespaces", len(namespacesSeen)),
		slog.Int("errors", len(lo.Filter(errs, func(err error, _ int) bool { return err != nil }))),
	)

	// Log errors again so they are at the bottom of console output
	for i, pvc := range pvcs {
		err := errs[i]
		if err != nil {
			slog.ErrorContext(ctx, "backup of PVC failed",
				slog.String("namespace", pvc.Namespace),
				slog.String("pvc", pvc.Name),
				slog.Any("err", err),
			)
		}
	}
}

func weekday() string {
	now := time.Now()
	switch now.Weekday() {
	case time.Monday:
		return "monday"
	case time.Tuesday:
		return "tuesday"
	case time.Wednesday:
		return "wednesday"
	case time.Thursday:
		return "thursday"
	case time.Friday:
		return "friday"
	case time.Saturday:
		return "saturday"
	default:
		return "sunday"
	}
}

func ignore1[T any, U any, V any](f func(t T) V) func(t T, u U) V {
	return func(t T, u U) V {
		return f(t)
	}
}
