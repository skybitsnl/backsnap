package main

import (
	"context"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func SelectPVCsForBackup(ctx context.Context, clientset kubernetes.Interface, namespaces []string, excludeNamespaces map[string]struct{}) ([]corev1.PersistentVolumeClaim, error) {
	var pvcs []corev1.PersistentVolumeClaim

	for _, namespace := range namespaces {
		result, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
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
