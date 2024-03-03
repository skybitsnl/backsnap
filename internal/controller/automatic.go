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

	logger.InfoContext(ctx, "next backup for pvc should occur at",
		slog.Time("next", nextBackup),
	)

	// If the next backup is in the future, schedule our next reconcile then
	if nextBackup.After(time.Now()) {
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: time.Until(nextBackup),
		}, nil
	}

	// Like jobs from cronjobs, give backup names a deterministic name to avoid duplicates
	backupName := fmt.Sprintf("%s-%d", pvc.Name, nextBackup.Unix())

	newBackup := &v1alpha1.PVCBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: pvc.Namespace,
		},
		Spec: v1alpha1.PVCBackupSpec{
			PVCName: pvc.Name,
			TTL:     metav1.Duration{Duration: time.Hour},
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
