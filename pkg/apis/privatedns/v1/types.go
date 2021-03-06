package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
	//"k8s.io/apimachinery/pkg/runtime"
)

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PrivateDNS is a specification for a DNS resource
type PrivateDNS struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PrivateDNSSpec `json:"spec"`
}

// DNSSpec ...
type PrivateDNSSpec struct {
	Label      string        `json:"label"`
	Domain     string        `json:"domain"`
	SRVPort    string        `json:"srv-por"`
	SRVProto   string        `json:"srv-protocol"`
	PodTimeout time.Duration `json:"pod-timeout"`
	Service    bool          `json:"service"`
	Subdomain  bool          `json:"subdomain"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// PrivateDNSList is a list of DNS resources
type PrivateDNSList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []PrivateDNS `json:"items"`
}
