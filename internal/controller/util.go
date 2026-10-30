package controller

import "time"

var (
	BackupScheduleAnnotation     = "backsnap.skyb.it/schedule"
	CurrentlyRestoringAnnotation = "backsnap.skyb.it/restoring"
)

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
