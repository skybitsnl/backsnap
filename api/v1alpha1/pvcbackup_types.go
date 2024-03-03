package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTES:
// - All fields must have json tags
// - Run `make` after editing this file to update the CRDs accordingly

type PVCBackupSpec struct {
	// Name of the PVC to back up. Must be in the same namespace.
	PVCName string `json:"pvc"`

	// How long will the backup object be retained after the backup completes.
	// If not set, the object will be retained for at least one day. The
	// controller will also always keep the last PVCBackup for a particular PVC
	// around, so that it knows when the last backup was completed.
	TTL metav1.Duration `json:"ttl,omitempty"`
}

// +kubebuilder:validation:Enum=Succeeded;Failed
type Result string

type PVCBackupStatus struct {
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
	Result     *Result      `json:"result,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

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
