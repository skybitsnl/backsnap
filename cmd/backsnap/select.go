package main

import (
	"context"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SelectPVCsForBackup(ctx context.Context, kclient client.Client, namespaces []string, excludeNamespaces map[string]struct{}) ([]corev1.PersistentVolumeClaim, error) {
	var pvcs []corev1.PersistentVolumeClaim

	for _, namespace := range namespaces {
		result := &corev1.PersistentVolumeClaimList{}
		if err := kclient.List(ctx, result, &client.ListOptions{Namespace: namespace}); err != nil {
			slog.ErrorContext(ctx, "failed listing PVCs", slog.Any("err", err))
			return nil, err
		}

		for _, pvc := range result.Items {
			if _, ok := pvc.Annotations[NoBackupAnnotation]; ok {
				// ignore PVCs with a "no-backup" annotation
				continue
			}

			if pvc.DeletionTimestamp != nil {
				// ignore PVCs that are being deleted
				continue
			}

			if _, ok := excludeNamespaces[pvc.Namespace]; ok {
				// ignore PVCs in excluded namespaces
				continue
			}

			pvcs = append(pvcs, pvc)
		}
	}

	return pvcs, nil
}
