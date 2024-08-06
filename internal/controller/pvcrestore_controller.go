package controller

import (
	"context"
	"log/slog"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/samber/lo"
	backsnapv1alpha1 "github.com/skybitsnl/backsnap/api/v1alpha1"
)

// PVCRestoreReconciler reconciles a PVCRestore object
type PVCRestoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Namespaces        []string
	ExcludeNamespaces []string
	BackupSettings    BackupSettings
}

// +kubebuilder:rbac:groups=backsnap.skyb.it,resources=pvcrestores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=backsnap.skyb.it,resources=pvcrestores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=backsnap.skyb.it,resources=pvcrestores/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// TODO: should be able to add a finalizer for a PVC which is being backed up?
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// nolint: gocyclo
func (r *PVCRestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := slog.With(
		slog.String("namespace", req.Namespace),
		slog.String("pvcrestore", req.Name),
	)

	if lo.Contains(r.ExcludeNamespaces, req.Namespace) {
		// namespace excluded
		return ctrl.Result{}, nil
	}
	if !lo.Contains(r.Namespaces, "") && !lo.Contains(r.Namespaces, req.Namespace) {
		// namespace not included
		return ctrl.Result{}, nil
	}

	var restore backsnapv1alpha1.PVCRestore
	if err := r.Get(ctx, req.NamespacedName, &restore); err != nil {
		if apierrors.IsNotFound(err) {
			// apparently we don't need to restore anymore
			return ctrl.Result{}, nil
		}
		logger.ErrorContext(ctx, "unable to fetch PVCRestore", slog.Any("err", err))
		return ctrl.Result{}, err
	}

	if !restore.Status.FinishedAt.IsZero() {
		if restore.Status.Result == nil || *restore.Status.Result != "Succeeded" {
			logger.ErrorContext(ctx, "pvcrestore failed, not reconciling - please clean it up yourself")
			return ctrl.Result{}, nil
		}

		// TODO: remove PVCRestore after TTL?
		return ctrl.Result{}, nil
	}

	if restore.Spec.TargetPVC == "" {
		restore.Spec.TargetPVC = restore.Spec.SourcePVC
	}

	// The PVC must either not exist, or be marked as "restoring" with the UID
	// of the PVCRestore object, otherwise we error out here
	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: req.Namespace,
		Name:      restore.Spec.TargetPVC,
	}, &pvc); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.ErrorContext(ctx, "unable to fetch PVC", slog.Any("err", err))
			return ctrl.Result{}, err
		}

		var volumeClass *string
		if r.BackupSettings.VolumeClass != "" {
			volumeClass = &r.BackupSettings.VolumeClass
		}
		pvc = corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      restore.Spec.TargetPVC,
				Namespace: req.Namespace,
				Annotations: map[string]string{
					CurrentlyRestoringAnnotation: string(restore.UID),
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: volumeClass,
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: restore.Spec.TargetPVCSize,
					},
				},
			},
		}
		// Do NOT set the PVCRestore as the owner of the PVC, or the PVC will be
		// destroyed when the PVCRestore is destroyed.
		if err := r.Create(ctx, &pvc); err != nil {
			logger.ErrorContext(ctx, "unable to create PVC", slog.Any("err", err))
			return ctrl.Result{}, err
		}

		logger.InfoContext(ctx, "created PVC to restore to", slog.String("pvc", pvc.ObjectMeta.Name))
	}

	if uid, ok := pvc.ObjectMeta.Annotations[CurrentlyRestoringAnnotation]; !ok || uid != string(restore.UID) {
		logger.ErrorContext(ctx, "PVC to restore to already exists - refusing to reconcile")
		restore.Status.FinishedAt = lo.ToPtr(metav1.Time{Time: time.Now()})
		restore.Status.Result = lo.ToPtr[backsnapv1alpha1.Result]("Failed")
		if err := r.Status().Update(ctx, &restore); err != nil {
			logger.ErrorContext(ctx, "Failed to update PVCRestore", slog.Any("err", err))
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
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

		if restore.Spec.SourceNamespace == "" {
			restore.Spec.SourceNamespace = restore.Namespace
		}
		if restore.Spec.SourceSnapshot == "" {
			restore.Spec.SourceSnapshot = "latest"
		}

		job = batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: restore.Namespace,
				Name:      restore.Name,
				Annotations: map[string]string{
					CurrentlyRestoringAnnotation: string(restore.UID),
				},
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
									ClaimName: pvc.Name,
								},
							},
						}},
						Containers: []corev1.Container{{
							Name:            "default",
							Image:           r.BackupSettings.Image,
							ImagePullPolicy: "Always",
							Command: []string{
								"restic", "restore",
								restore.Spec.SourceSnapshot,
								"--sparse", "--verify",
								"--target", "/data",
							},
							Env: []corev1.EnvVar{{
								Name:  "BACKUP_NAMESPACE",
								Value: restore.Spec.SourceNamespace,
							}, {
								Name:  "BACKUP_VOLUME",
								Value: restore.Spec.SourcePVC,
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
							// We mount the volume at /data/data, and then restore to /data, because the
							// topmost directory of the backup will be /data so will end up at /data/data
							// and therefore in the root of the PVC
							VolumeMounts: []corev1.VolumeMount{{
								Name:      "data",
								MountPath: "/data/data",
							}},
						}},
					},
				},
			},
		}
		if err := ctrl.SetControllerReference(&restore, &job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, &job); err != nil {
			return ctrl.Result{}, err
		}

		logger.InfoContext(ctx, "created restore job",
			slog.String("name", job.Name),
		)
	}

	if uid, ok := job.ObjectMeta.Annotations[CurrentlyRestoringAnnotation]; !ok || uid != string(restore.UID) {
		logger.ErrorContext(ctx, "Restore job already exists - refusing to reconcile")
		restore.Status.FinishedAt = lo.ToPtr(metav1.Time{Time: time.Now()})
		restore.Status.Result = lo.ToPtr[backsnapv1alpha1.Result]("Failed")
		if err := r.Status().Update(ctx, &restore); err != nil {
			logger.ErrorContext(ctx, "Failed to update PVCRestore", slog.Any("err", err))
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// TODO: we could tail the job's log here for ease of access

	if job.Status.CompletionTime == nil || job.Status.CompletionTime.IsZero() {
		logger.InfoContext(ctx, "restore job is running",
			slog.String("name", job.Name),
		)

		// Wait for the job to complete. We'll automatically get another reconcile
		// when the job changes.
		return ctrl.Result{}, nil
	}

	logger.InfoContext(ctx, "restore job succeeded",
		slog.String("name", req.Name))

	// Remove the restore annotation from the PVC
	delete(pvc.ObjectMeta.Annotations, CurrentlyRestoringAnnotation)
	if err := r.Update(ctx, &pvc); err != nil {
		logger.ErrorContext(ctx, "Failed to remove annotation from PVC", slog.Any("err", err))
		restore.Status.FinishedAt = lo.ToPtr(metav1.Time{Time: time.Now()})
		restore.Status.Result = lo.ToPtr[backsnapv1alpha1.Result]("Failed")
		if err := r.Status().Update(ctx, &restore); err != nil {
			logger.ErrorContext(ctx, "Failed to update PVCRestore", slog.Any("err", err))
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	restore.Status.FinishedAt = lo.ToPtr(metav1.Time{Time: time.Now()})
	restore.Status.Result = lo.ToPtr[backsnapv1alpha1.Result]("Succeeded")
	if err := r.Status().Update(ctx, &restore); err != nil {
		logger.ErrorContext(ctx, "Failed to update PVCRestore", slog.Any("err", err))
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PVCRestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backsnapv1alpha1.PVCRestore{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
