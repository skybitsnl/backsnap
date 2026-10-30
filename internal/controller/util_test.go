package controller

import (
	"testing"

	"github.com/samber/lo"
)

func TestStorageClassName(t *testing.T) {
	settings := func(general, backup, restore string) BackupSettings {
		return BackupSettings{StorageClass: general, BackupStorageClass: backup, RestoreStorageClass: restore}
	}

	tests := []struct {
		rule        string
		settings    BackupSettings
		override    string
		wantBackup  *string
		wantRestore *string
	}{{
		rule:        "nothing set means the cluster default",
		settings:    settings("", "", ""),
		wantBackup:  nil,
		wantRestore: nil,
	}, {
		rule:        "the general StorageClass is used for both",
		settings:    settings("general", "", ""),
		wantBackup:  lo.ToPtr("general"),
		wantRestore: lo.ToPtr("general"),
	}, {
		rule:        "specific StorageClasses win over the general one",
		settings:    settings("general", "backup", "restore"),
		wantBackup:  lo.ToPtr("backup"),
		wantRestore: lo.ToPtr("restore"),
	}, {
		rule:        "the object's StorageClass wins over all settings",
		settings:    settings("general", "backup", "restore"),
		override:    "object",
		wantBackup:  lo.ToPtr("object"),
		wantRestore: lo.ToPtr("object"),
	}, {
		rule:        "the object's StorageClass also wins without settings",
		settings:    settings("", "", ""),
		override:    "object",
		wantBackup:  lo.ToPtr("object"),
		wantRestore: lo.ToPtr("object"),
	}}

	for _, tc := range tests {
		t.Run(tc.rule, func(t *testing.T) {
			if got := tc.settings.backupStorageClassName(tc.override); !equalPtr(got, tc.wantBackup) {
				t.Errorf("backup: got %v, want %v", fmtPtr(got), fmtPtr(tc.wantBackup))
			}
			if got := tc.settings.restoreStorageClassName(tc.override); !equalPtr(got, tc.wantRestore) {
				t.Errorf("restore: got %v, want %v", fmtPtr(got), fmtPtr(tc.wantRestore))
			}
		})
	}
}

func equalPtr(a, b *string) bool {
	return lo.FromPtr(a) == lo.FromPtr(b) && (a == nil) == (b == nil)
}

func fmtPtr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
