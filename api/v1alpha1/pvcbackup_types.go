package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTES:
// - All fields must have json tags
// - Run `make` after editing this file to update the CRDs accordingly

type PVCBackupSpec struct {
	// Name of the PVC to back up. Must be in the same namespace.
	PVCName string `json:"pvc"`

	// How long will the backup object be retained after the backup completes.
	// The controller will also always keep the last PVCBackup for a particular
	// PVC around, so that it knows when the last backup was completed.
	TTL metav1.Duration `json:"ttl,omitempty"`

	// NodeSelector is a selector which must be true for the backup Pod to fit
	// on a node. This can be used e.g. to select which type of node, or which
	// Availability Zone, performs a backup.
	// More info: https://kubernetes.io/docs/concepts/configuration/assign-pod-node/
	// +optional
	// +mapType=atomic
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// If specified, the backup Pod's tolerations.
	// +optional
	// +listType=atomic
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// If specified, indicates the backup Pod's priority.
	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// If specified, indicates the labels to be put on the backup
	// VolumeSnapshot, backup temporary PVC, backup Job and backup Pod.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// If specified, indicates the annotations to be put on the backup
	// VolumeSnapshot, backup temporary PVC, backup Job and backup Pod. This
	// SHOULD NOT include any backsnap.skyb.it annotations.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// +kubebuilder:validation:Enum=Succeeded;Failed
type Result string

type PVCBackupStatus struct {
	StartedAt  *metav1.Time     `json:"startedAt,omitempty"`
	FinishedAt *metav1.Time     `json:"finishedAt,omitempty"`
	Duration   *metav1.Duration `json:"duration,omitempty"`
	Result     *Result          `json:"result,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time the backup was requested"
//+kubebuilder:printcolumn:name="Started at",type="string",JSONPath=".status.startedAt",description="Time the backup job started running"
//+kubebuilder:printcolumn:name="Duration",type="string",JSONPath=".status.duration",description="Time the backup job took to finish running"
//+kubebuilder:printcolumn:name="Result",type="string",JSONPath=".status.result",description="Shows whether the backup succeeded or not"

// PVCBackup is the Schema for the pvcbackups API
type PVCBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PVCBackupSpec   `json:"spec,omitempty"`
	Status PVCBackupStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PVCBackupList contains a list of PVCBackup
type PVCBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PVCBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PVCBackup{}, &PVCBackupList{})
}
