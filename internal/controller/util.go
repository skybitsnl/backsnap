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
	SnapshotClass   string
	StorageClass    string
	ImagePullSecret string
	Image           string
	// S3 hostname (can be host, host:port or http://host:port/)
	S3Host            string
	S3Bucket          string
	S3AccessKeyId     string
	S3SecretAccessKey string
	ResticPassword    string
}
