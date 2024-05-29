package controller

import (
	"context"
	"log/slog"
	"time"

	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/samber/lo"
	v1alpha1 "github.com/skybitsnl/backsnap/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PVCBackupReconciler reconciles a PVCBackup object
type PVCBackupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Clock
	Namespaces        []string
	ExcludeNamespaces []string
	BackupSettings    BackupSettings

	// TODO: need to add a boolean whether we are currently running any backups.
	// If so, other backups need to wait until that one is done (and ideally, wait
	// a configurable amount longer to reduce load on the storage layer).
}

// TODO: these next roles allow creating and deleting PVCs and jobs anywhere.
// Once cross-namespace data sources are implemented, this SA only needs rights
// to create and delete PVCs and jobs in the backsnap namespace, which is much
// safer.
// https://kubernetes.io/blog/2023/01/02/cross-namespace-data-sources-alpha/

//+kubebuilder:rbac:groups=backsnap.skyb.it,resources=pvcbackups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=backsnap.skyb.it,resources=pvcbackups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=backsnap.skyb.it,resources=pvcbackups/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// TODO: should be able to add a finalizer for a PVC which is being backed up?
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims/finalizers,verbs=update
//+kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// nolint: gocyclo
func (r *PVCBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := slog.With(
		slog.String("namespace", req.Namespace),
		slog.String("pvcbackup", req.Name),
	)

	if lo.Contains(r.ExcludeNamespaces, req.Namespace) {
		// namespace excluded
		return ctrl.Result{}, nil
	}
	if !lo.Contains(r.Namespaces, "") && !lo.Contains(r.Namespaces, req.Namespace) {
		// namespace not included
		return ctrl.Result{}, nil
	}

	var backup v1alpha1.PVCBackup
	if err := r.Get(ctx, req.NamespacedName, &backup); err != nil {
		if apierrors.IsNotFound(err) {
			// ignore not-found errors since we don't need to create any PVCBackup for those
			return ctrl.Result{}, nil
		}
		logger.ErrorContext(ctx, "unable to fetch PVCBackup", slog.Any("err", err))
		return ctrl.Result{}, err
	}

	logger = logger.With(slog.String("pvc", backup.Spec.PVCName))

	if !backup.Status.FinishedAt.IsZero() {
		if backup.Status.Result == nil || *backup.Status.Result != "Succeeded" {
			logger.ErrorContext(ctx, "pvcbackup failed, not reconciling - please clean it up yourself")
			return ctrl.Result{}, nil
		}

		deletionAt := backup.Status.FinishedAt.Add(backup.Spec.TTL.Duration)
		timeUntilDeletion := deletionAt.Sub(r.Clock.Now())

		if timeUntilDeletion <= 0 {
			// Check whether this PVCBackup is the last to exist for this PVC

			siblings := &v1alpha1.PVCBackupList{}
			if err := r.List(ctx, siblings, client.InNamespace(backup.Namespace)); err != nil {
				return ctrl.Result{}, err
			}

			var hasOlderSiblings bool
			for _, sibling := range siblings.Items {
				if sibling.Spec.PVCName == backup.Spec.PVCName && sibling.Status.FinishedAt != nil && sibling.Status.FinishedAt.After(backup.Status.FinishedAt.Time) {
					hasOlderSiblings = true
					break
				}
			}

			if !hasOlderSiblings {
				logger.ErrorContext(ctx, "wanted to remove pvcbackup, but it is the last to finish, skipping")
				return ctrl.Result{
					Requeue:      true,
					RequeueAfter: backup.Spec.TTL.Duration,
				}, nil
			}

			logger.InfoContext(ctx, "deleting finished pvcbackup, since its TTL expired")

			if err := r.Delete(ctx, &backup); err != nil {
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}

		logger.InfoContext(ctx, "performing cleanup of a finished pvcbackup")

		// Clean up
		if err := r.Delete(ctx, &volumesnapshotv1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: backup.Namespace,
				Name:      backup.Name,
			},
		}, &client.DeleteOptions{
			PropagationPolicy: lo.ToPtr(metav1.DeletePropagationBackground),
		}); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if err := r.Delete(ctx, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: backup.Namespace,
				Name:      backup.Name,
			},
		}, &client.DeleteOptions{
			PropagationPolicy: lo.ToPtr(metav1.DeletePropagationBackground),
		}); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if err := r.Delete(ctx, &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: backup.Namespace,
				Name:      backup.Name,
			},
		}, &client.DeleteOptions{
			PropagationPolicy: lo.ToPtr(metav1.DeletePropagationBackground),
		}); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		// TODO: log backup history for this PVC, snapshot moment, duration, bytes copied, bytes to restore, total bytes
		// TODO: write metrics for the number of failed PVCBackup objects
		// - or document in README how to get these metrics from the k8s API server directly

		// Requeue for deletion after TTL expires
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: timeUntilDeletion,
		}, nil
	}

	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: req.Namespace,
		Name:      backup.Spec.PVCName,
	}, &pvc); err != nil {
		logger.ErrorContext(ctx, "unable to fetch PVC", slog.Any("err", err))
		return ctrl.Result{}, err
	}

	// Retrieve or create the VolumeSnapshot
	var snapshot volumesnapshotv1.VolumeSnapshot
	if err := r.Get(ctx, req.NamespacedName, &snapshot); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.ErrorContext(ctx, "unable to fetch snapshot", slog.Any("err", err))
			return ctrl.Result{}, err
		}

		var snapshotClass *string
		if r.BackupSettings.SnapshotClass != "" {
			snapshotClass = &r.BackupSettings.SnapshotClass
		}

		snapshot = volumesnapshotv1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: backup.Namespace,
				Name:      backup.Name,
			},
			Spec: volumesnapshotv1.VolumeSnapshotSpec{
				Source: volumesnapshotv1.VolumeSnapshotSource{
					PersistentVolumeClaimName: &pvc.Name,
				},
				VolumeSnapshotClassName: snapshotClass,
			},
		}
		if err := ctrl.SetControllerReference(&backup, &snapshot, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, &snapshot); err != nil {
			return ctrl.Result{}, err
		}

		logger.InfoContext(ctx, "created volumesnapshot",
			slog.String("name", snapshot.Name),
		)
	}

	// TODO: if there is something wrong with this VolumeSnapshot (e.g. no snapshot class is given and there
	// is no default snapshot class), this will simply wait forever, even if there are Warning events on the
	// VolumeSnapshot right away. Between creation and readiness of the VolumeSnapshot, should we log all events
	// that appear on it?

	if snapshot.Status == nil || snapshot.Status.ReadyToUse == nil || !*snapshot.Status.ReadyToUse {
		// Wait for the snapshot to become ReadyToUse. We'll automatically get another reconcile
		// when the snapshot changes.
		return ctrl.Result{}, nil
	}

	logger.InfoContext(ctx, "volumesnapshot is ready",
		slog.String("name", snapshot.Name),
		slog.String("size", snapshot.Status.RestoreSize.String()),
	)

	// Retrieve or create the snapshot PVC
	var snapshotPvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, req.NamespacedName, &snapshotPvc); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.ErrorContext(ctx, "unable to fetch snapshot PVC", slog.Any("err", err))
			return ctrl.Result{}, err
		}

		var volumeClass *string
		if r.BackupSettings.VolumeClass != "" {
			volumeClass = &r.BackupSettings.VolumeClass
		}
		snapshotPvc = corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: backup.Namespace,
				Name:      backup.Name,
				Annotations: map[string]string{
					BackupScheduleAnnotation: "",
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: volumeClass,
				DataSource: &corev1.TypedLocalObjectReference{
					APIGroup: lo.ToPtr("snapshot.storage.k8s.io"),
					Kind:     "VolumeSnapshot",
					Name:     snapshot.Name,
				},
				// TODO: would prefer to use ReadOnlyMany but not all providers support it
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: *snapshot.Status.RestoreSize,
					},
				},
			},
		}
		if err := ctrl.SetControllerReference(&backup, &snapshotPvc, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, &snapshotPvc); err != nil {
			return ctrl.Result{}, err
		}

		logger.InfoContext(ctx, "created snapshot PVC",
			slog.String("name", snapshotPvc.Name),
		)
	}

	var job batchv1.Job
	if err := r.Get(ctx, req.NamespacedName, &job); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.ErrorContext(ctx, "unable to fetch job", slog.Any("err", err))
			return ctrl.Result{}, err
		}

		var imagePullSecrets []corev1.LocalObjectReference
		if r.BackupSettings.ImagePullSecret != "" {
			imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{
				Name: r.BackupSettings.ImagePullSecret,
			})
		}

		job = batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: backup.Namespace,
				Name:      backup.Name,
			},
			Spec: batchv1.JobSpec{
				BackoffLimit: lo.ToPtr(int32(4)),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						ImagePullSecrets: imagePullSecrets,
						RestartPolicy:    "OnFailure",
						Volumes: []corev1.Volume{{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: snapshotPvc.Name,
									ReadOnly:  true,
								},
							},
						}},
						Containers: []corev1.Container{{
							Name:            "default",
							Image:           r.BackupSettings.Image,
							ImagePullPolicy: "Always",
							Env: []corev1.EnvVar{{
								Name:  "BACKUP_NAMESPACE",
								Value: pvc.Namespace,
							}, {
								Name:  "BACKUP_VOLUME",
								Value: pvc.Name,
							}, {
								Name:  "RESTIC_REPOSITORY_BASE",
								Value: "s3:" + r.BackupSettings.S3Host + "/" + r.BackupSettings.S3Bucket,
							}, {
								Name:  "RESTIC_PASSWORD",
								Value: r.BackupSettings.ResticPassword,
							}, {
								Name:  "AWS_ACCESS_KEY_ID",
								Value: r.BackupSettings.S3AccessKeyId,
							}, {
								Name:  "AWS_SECRET_ACCESS_KEY",
								Value: r.BackupSettings.S3SecretAccessKey,
							}, {
								Name:  "RESTIC_HOSTNAME",
								Value: "$(BACKUP_NAMESPACE)",
							}, {
								Name:  "RESTIC_REPOSITORY",
								Value: "$(RESTIC_REPOSITORY_BASE)/$(BACKUP_NAMESPACE)/$(BACKUP_VOLUME)",
							}},
							VolumeMounts: []corev1.VolumeMount{{
								Name:      "data",
								ReadOnly:  true,
								MountPath: "/data",
							}},
						}},
					},
				},
			},
		}
		if err := ctrl.SetControllerReference(&backup, &job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, &job); err != nil {
			return ctrl.Result{}, err
		}

		logger.InfoContext(ctx, "created backup job",
			slog.String("name", job.Name),
		)
	}

	// TODO: we could tail the job's log here for ease of access

	if job.Status.CompletionTime == nil || job.Status.CompletionTime.IsZero() {
		logger.InfoContext(ctx, "backup job is running",
			slog.String("name", job.Name),
		)

		// Wait for the job to complete. We'll automatically get another reconcile
		// when the job changes.
		return ctrl.Result{}, nil
	}

	logger.InfoContext(ctx, "backup job succeeded",
		slog.String("name", job.Name),
	)
	backup.Status.FinishedAt = lo.ToPtr(metav1.Time{Time: time.Now()})
	backup.Status.Result = lo.ToPtr[v1alpha1.Result]("Succeeded")
	if err := r.Status().Update(ctx, &backup); err != nil {
		logger.ErrorContext(ctx, "Failed to update PVCBackup", slog.Any("err", err))
		return ctrl.Result{}, err
	}

	// Reconcile ourselves immediately to clean up
	return ctrl.Result{Requeue: true}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PVCBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clock == nil {
		r.Clock = realClock{}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PVCBackup{}).
		Owns(&volumesnapshotv1.VolumeSnapshot{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
