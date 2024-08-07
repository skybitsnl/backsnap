package controller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hashicorp/cronexpr"
	"github.com/samber/lo"
	"github.com/skybitsnl/backsnap/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	jobOwnerKey = ".metadata.controller"

	// Sometimes, leap seconds or time differences between systems may cause a
	// backup to exist *just* before its schedule. For example, a situation has
	// been observed where a backup was scheduled at 12:00:00 UTC, but its
	// eventual creationDate was 11:59:59 UTC. This then causes another backup
	// to be executed at 12:00:00, i.e. two backups within a very small time
	// interval. To prevent this, we have a certain tolerance that allows a
	// backup to be created just before its scheduled time, to act as a backup
	// considered at that scheduled time. This is expressed as a percentage of
	// the normal interval.
	// For example, if the schedule is @hourly, the normal interval is 3600
	// seconds. A tolerance factor of 1% (0.01) means a tolerance of 36 seconds,
	// i.e. a backup created after 11:59:24 or later will be considered scheduled
	// at 12:00:00, so the next backup will be created at 13:00:00 in this case.
	ScheduleIntervalTolerance = 0.01

	// Default time-to-live for newly created backups: 3 days.
	DefaultTTL = 3 * 24 * time.Hour
)

type AutomaticPVCBackupCreator struct {
	client.Client
	Scheme *runtime.Scheme
	Clock
	DefaultSchedule   string
	Namespaces        []string
	ExcludeNamespaces []string
}

//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims/finalizers,verbs=update
//+kubebuilder:rbac:groups=backsnap.skyb.it,resources=pvcbackups,verbs=get;list;watch;create;update;patch

func (r *AutomaticPVCBackupCreator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := slog.With(
		slog.String("namespace", req.Namespace),
		slog.String("pvc", req.Name),
	)

	var namespace corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Namespace}, &namespace); err != nil {
		if apierrors.IsNotFound(err) {
			// ignore not-found errors since we don't need to create any PVCBackup for those
			return ctrl.Result{}, nil
		}
		logger.ErrorContext(ctx, "unable to fetch namespace", slog.Any("err", err))
		return ctrl.Result{}, err
	}

	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, req.NamespacedName, &pvc); err != nil {
		if apierrors.IsNotFound(err) {
			// ignore not-found errors since we don't need to create any PVCBackup for those
			return ctrl.Result{}, nil
		}
		logger.ErrorContext(ctx, "unable to fetch PVC", slog.Any("err", err))
		return ctrl.Result{}, err
	}

	// Find the schedule for this PVC, from its annotations, namespace annotations or default flags.
	var schedule string
	if s, ok := pvc.Annotations[BackupScheduleAnnotation]; ok {
		schedule = s
	} else if s, ok := namespace.Annotations[BackupScheduleAnnotation]; ok {
		schedule = s
	} else {
		schedule = r.DefaultSchedule
	}

	if schedule == "" {
		logger.InfoContext(ctx, "ignoring PVC for backup because schedule is empty")
		return ctrl.Result{}, nil
	}

	parsedSchedule, err := cronexpr.Parse(schedule)
	if err != nil {
		logger.ErrorContext(ctx, "unable to parse schedule expression",
			slog.String("cron", schedule),
			slog.Any("err", err),
		)
		return ctrl.Result{}, err
	}

	// Find the last backup for this PVC, or its created date otherwise
	var childBackups v1alpha1.PVCBackupList
	if err := r.List(ctx, &childBackups, client.InNamespace(pvc.Namespace), client.MatchingFields{jobOwnerKey: pvc.Name}); err != nil {
		logger.ErrorContext(ctx, "unable to list PVC child backups", slog.Any("err", err))
		return ctrl.Result{}, err
	}

	newestBackup := lo.Reduce(childBackups.Items, func(newest time.Time, item v1alpha1.PVCBackup, _ int) time.Time {
		if item.CreationTimestamp.Time.After(newest) {
			return item.CreationTimestamp.Time
		} else {
			return newest
		}
	}, pvc.CreationTimestamp.Time)

	nextBackup := parsedSchedule.Next(newestBackup)

	// If the nextBackup is very close to the last backup, less than a low
	// percentage of the expected interval, then skip the next backup. See the
	// documentation next to ScheduleIntervalTolerance.
	actualInterval := nextBackup.Sub(newestBackup)
	scheduleInterval := parsedSchedule.Next(nextBackup).Sub(nextBackup)
	if float64(actualInterval) < float64(scheduleInterval)*ScheduleIntervalTolerance {
		logger.InfoContext(ctx, "the most-recent backup is within tolerance of a more recent scheduled backup - skipping that scheduled backup",
			slog.Time("mostrecent", newestBackup),
			slog.Time("scheduled", nextBackup),
		)
		nextBackup = parsedSchedule.Next(nextBackup)
	}

	logger.InfoContext(ctx, "next backup for pvc should occur at",
		slog.Time("next", nextBackup),
	)

	// If the next backup is in the future, schedule our next reconcile then
	now := time.Now()
	if nextBackup.After(now) {
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: time.Until(nextBackup),
		}, nil
	}

	// Give backup names a unique name containing their scheduled unix timestamps.
	backupName := fmt.Sprintf("%s-%d", pvc.Name, now.Unix())

	newBackup := &v1alpha1.PVCBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: pvc.Namespace,
		},
		Spec: v1alpha1.PVCBackupSpec{
			PVCName: pvc.Name,
			TTL:     metav1.Duration{Duration: DefaultTTL},
		},
	}
	if err := ctrl.SetControllerReference(&pvc, newBackup, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, newBackup); err != nil {
		logger.ErrorContext(ctx, "failed to create PVCBackup", slog.Any("err", err))
		return ctrl.Result{}, err
	}

	logger.InfoContext(ctx, "created new PVCBackup", slog.Any("name", newBackup.ObjectMeta.Name))

	// After that, schedule for the next run after this one
	nextBackup = parsedSchedule.Next(newBackup.CreationTimestamp.Time)
	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: time.Until(nextBackup),
	}, nil
}

func (r *AutomaticPVCBackupCreator) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clock == nil {
		r.Clock = realClock{}
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.PVCBackup{}, jobOwnerKey, func(rawObj client.Object) []string {
		pvc := rawObj.(*v1alpha1.PVCBackup)
		owner := metav1.GetControllerOf(pvc)
		if owner == nil {
			return nil
		}
		// Make sure it's a PVC
		if owner.APIVersion != corev1.SchemeGroupVersion.String() || owner.Kind != "PersistentVolumeClaim" {
			return nil
		}
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("automatic").
		Watches(
			&corev1.PersistentVolumeClaim{},
			handler.EnqueueRequestsFromMapFunc(r.pvcToRequest),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Watches(
			&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(r.namespaceToRequests),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Complete(r)
}

func (r *AutomaticPVCBackupCreator) pvcToRequest(ctx context.Context, pvc client.Object) []reconcile.Request {
	if lo.Contains(r.ExcludeNamespaces, pvc.GetNamespace()) {
		// namespace excluded
		return []reconcile.Request{}
	}
	if !lo.Contains(r.Namespaces, "") && !lo.Contains(r.Namespaces, pvc.GetNamespace()) {
		// namespace not included
		return []reconcile.Request{}
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Name:      pvc.GetName(),
			Namespace: pvc.GetNamespace(),
		},
	}}
}

func (r *AutomaticPVCBackupCreator) namespaceToRequests(ctx context.Context, namespace client.Object) []reconcile.Request {
	if lo.Contains(r.ExcludeNamespaces, namespace.GetName()) {
		// namespace excluded
		return []reconcile.Request{}
	}
	if !lo.Contains(r.Namespaces, "") && !lo.Contains(r.Namespaces, namespace.GetName()) {
		// namespace not included
		return []reconcile.Request{}
	}

	var pvcList corev1.PersistentVolumeClaimList
	if err := r.List(ctx, &pvcList, client.InNamespace(namespace.GetName())); err != nil {
		slog.Error("failed to enumerate PVCs in namespace",
			slog.String("namespace", namespace.GetName()),
			slog.Any("err", err),
		)
		return []reconcile.Request{}
	}

	return lo.Map(pvcList.Items, func(pvc corev1.PersistentVolumeClaim, _ int) reconcile.Request {
		return reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      pvc.GetName(),
				Namespace: pvc.GetNamespace(),
			},
		}
	})
}
