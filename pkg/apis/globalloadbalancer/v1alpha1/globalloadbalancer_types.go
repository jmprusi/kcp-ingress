/*
Copyright 2021 The Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GlobalLoadBalancer describes an instance of a Global LoadBalancer.
//
// +crd
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=kcp-ingress
// +kubebuilder:printcolumn:name="Location",type="string",JSONPath=`.metadata.clusterName`,priority=1
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`,priority=2
// +kubebuilder:printcolumn:name="Accepted",type="string",JSONPath=`.status.conditions[?(@.type=="Accepted")].status`,priority=3
// +kubebuilder:printcolumn:name="Hostname",type="string",JSONPath=`.status.hostname`,priority=4

type GlobalLoadBalancer struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec holds the desired state.
	// +optional
	Spec GlobalLoadBalancerSpec `json:"spec,omitempty"`

	// Status communicates the observed state.
	// +optional
	Status GlobalLoadBalancerStatus `json:"status,omitempty"`
}

//TODO: Really basic CRD. Will do for now.

// GlobalLoadBalancerSpec
type GlobalLoadBalancerSpec struct {
	// +optional
	RequestedHostnames []string `json:"requested_hostnames"`
	// +optional
	Endpoints []Endpoint `json:"endpoints"`
}

// Endpoint is the destination
type Endpoint struct {
	IP       string `json:"ip,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// GlobalLoadBalancerStatus
type GlobalLoadBalancerStatus struct {
	Conditions Conditions `json:"conditions,omitempty"`
	Hostname   string     `json:"hostname,omitempty"`
}

// TODO(jmprusi): Create a generic SetCondition
func (glb *GlobalLoadBalancerStatus) SetConditionReady(status corev1.ConditionStatus, reason, message string) {
	for idx, cond := range glb.Conditions {
		if cond.Type == GlobalLoadBalancerConditionReady {
			glb.Conditions[idx] = Condition{
				Type:               GlobalLoadBalancerConditionReady,
				Status:             status,
				Reason:             reason,
				Message:            message,
				LastTransitionTime: cond.LastTransitionTime,
			}
			return
		}
	}
	glb.Conditions = append(glb.Conditions, Condition{
		Type:               GlobalLoadBalancerConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
}

// TODO(jmprusi): Create a generic SetCondition
func (glb *GlobalLoadBalancerStatus) SetConditionAccepted(status corev1.ConditionStatus, reason, message string) {
	for idx, cond := range glb.Conditions {
		if cond.Type == GlobalLoadBalancerConditionAccepted {
			glb.Conditions[idx] = Condition{
				Type:               GlobalLoadBalancerConditionAccepted,
				Status:             status,
				Reason:             reason,
				Message:            message,
				LastTransitionTime: cond.LastTransitionTime,
			}
			return
		}
	}
	glb.Conditions = append(glb.Conditions, Condition{
		Type:               GlobalLoadBalancerConditionAccepted,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
}

// GlobalLoadBalancerList is a list of GlobalLoadBalancers resources
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type GlobalLoadBalancerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []GlobalLoadBalancer `json:"items"`
}
