package controller

import (
	"slices"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	BackupScheduleAnnotation     = "backsnap.skyb.it/schedule"
	CurrentlyRestoringAnnotation = "backsnap.skyb.it/restoring"
)

// The name of the volume in backup and restore Pods that holds the data.
const dataVolume = "data"

// requeueAfter reconciles again after d. If d has already passed, for example
// because a schedule was computed from a timestamp in the past, it reconciles
// again after a second instead.
func requeueAfter(d time.Duration) ctrl.Result {
	return ctrl.Result{RequeueAfter: max(d, time.Second)}
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Clock interface {
	Now() time.Time
}

type BackupSettings struct {
	SnapshotClass string
	// StorageClass is used for both backup and restore PVCs, unless overridden
	// by BackupStorageClass or RestoreStorageClass
	StorageClass        string
	BackupStorageClass  string
	RestoreStorageClass string
	ImagePullSecret     string
	Image               string
	// S3 hostname (can be host, host:port or http://host:port/)
	S3Host            string
	S3Bucket          string
	S3AccessKeyId     string
	S3SecretAccessKey string
	ResticPassword    string
	// While rotating the restic password, the previous password. Backups move
	// repositories that still use it to ResticPassword.
	ResticOldPassword string
	ResticEnv         map[string]string
	// Resources of backup and restore containers
	ResticResources corev1.ResourceRequirements
}

// defaultResticEnv contains defaults for variables in backup and restore
// containers, which can be overridden by ResticEnv.
var defaultResticEnv = map[string]string{
	// How long restic waits for a lock on the repository (restic --retry-lock)
	"BACKSNAP_RETRY_LOCK": "30m",
	// Which part of the data the check after a backup reads (restic check
	// --read-data-subset)
	"BACKSNAP_CHECK_SUBSET": "10%",
	// Log the progress of restic once per minute; without this, restic shows no
	// progress when its output is not a terminal
	"RESTIC_PROGRESS_FPS": "0.0166666",
}

// resticEnv returns the environment for a backup or restore container: the
// variables managed by the operator, followed by the configured ResticEnv and
// the defaults. Managed variables cannot be overridden by ResticEnv.
func (s BackupSettings) resticEnv(managed []corev1.EnvVar) []corev1.EnvVar {
	extra := lo.Assign(defaultResticEnv, s.ResticEnv)
	names := lo.Keys(extra)
	slices.Sort(names)

	env := managed
	for _, name := range names {
		if !lo.ContainsBy(managed, func(e corev1.EnvVar) bool { return e.Name == name }) {
			env = append(env, corev1.EnvVar{Name: name, Value: extra[name]})
		}
	}
	return env
}

// backupStorageClassName returns the StorageClass for a temporary backup PVC,
// or nil to use the cluster default. The override comes from the PVCBackup.
func (s BackupSettings) backupStorageClassName(override string) *string {
	return firstStorageClassName(override, s.BackupStorageClass, s.StorageClass)
}

// restoreStorageClassName returns the StorageClass for a restored PVC, or nil
// to use the cluster default. The override comes from the PVCRestore.
func (s BackupSettings) restoreStorageClassName(override string) *string {
	return firstStorageClassName(override, s.RestoreStorageClass, s.StorageClass)
}

func firstStorageClassName(names ...string) *string {
	for _, name := range names {
		if name != "" {
			return &name
		}
	}
	return nil
}
