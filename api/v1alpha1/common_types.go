/*
Copyright 2026.

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

// NamespacedObjectReference identifies another object by name and, optionally,
// namespace. When Namespace is empty, it defaults to the referencing object's
// own namespace (see docs/ARCHITECTURE.md Decision 2). Also used for back-ref
// status lists (Decision 5), where Namespace is always populated.
type NamespacedObjectReference struct {
	// name of the referenced object.
	// +required
	Name string `json:"name"`

	// namespace of the referenced object. Defaults to the referencing object's
	// own namespace when omitted. Explicit default (rather than merely
	// +optional) is required because this field is also used as a
	// x-kubernetes-list-map-key on status back-ref lists (Decision 5), which
	// the Kubernetes API server requires to have a default or be required.
	// +optional
	// +kubebuilder:default=""
	Namespace string `json:"namespace,omitempty"`
}

// Condition type strings shared across panoptikum.dev CRDs. Values match the
// Gateway API convention (see docs/ARCHITECTURE.md "Reconciliation design"),
// reused here as plain string constants rather than importing the gateway-api
// module for two names.
const (
	// ConditionTypeReady indicates whether the resource is fully reconciled and operational.
	ConditionTypeReady = "Ready"

	// ConditionTypeResolvedRefs indicates whether all of a resource's own
	// references (to other objects) were found.
	ConditionTypeResolvedRefs = "ResolvedRefs"

	// ConditionTypeAccepted indicates whether a referenced target resource
	// has bound the referencing resource (see docs/ARCHITECTURE.md Decision 5).
	ConditionTypeAccepted = "Accepted"
)
