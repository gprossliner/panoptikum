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

// Shared cross-reference resolution helpers used by multiple reconcilers.
package controller

import (
	"github.com/gprossliner/xhdl"
	"sigs.k8s.io/controller-runtime/pkg/client"

	panoptikumv1alpha1 "github.com/gprossliner/panoptikum/api/v1alpha1"
	"github.com/gprossliner/panoptikum/internal/apicall"
)

// resolveNamespace returns refNamespace, defaulting to ownNamespace when
// refNamespace is empty (see docs/ARCHITECTURE.md Decision 2).
func resolveNamespace(ownNamespace, refNamespace string) string {
	if refNamespace == "" {
		return ownNamespace
	}
	return refNamespace
}

func refString(ownNamespace string, ref panoptikumv1alpha1.NamespacedObjectReference) string {
	return resolveNamespace(ownNamespace, ref.Namespace) + "/" + ref.Name
}

func serviceRefString(ownNamespace string, svc panoptikumv1alpha1.ServiceBackend) string {
	return resolveNamespace(ownNamespace, svc.Namespace) + "/" + svc.Name
}

func getPortal(ctx xhdl.Context, cl client.Client, ownNamespace string, ref panoptikumv1alpha1.NamespacedObjectReference) (*panoptikumv1alpha1.Portal, bool) {
	var portal panoptikumv1alpha1.Portal
	if !apicall.ApiTryGet(ctx, cl, client.ObjectKey{Name: ref.Name, Namespace: resolveNamespace(ownNamespace, ref.Namespace)}, &portal) {
		return nil, false
	}
	return &portal, true
}

func getAppAuthentication(ctx xhdl.Context, cl client.Client, ownNamespace string, ref panoptikumv1alpha1.NamespacedObjectReference) (*panoptikumv1alpha1.AppAuthentication, bool) {
	var appAuth panoptikumv1alpha1.AppAuthentication
	if !apicall.ApiTryGet(ctx, cl, client.ObjectKey{Name: ref.Name, Namespace: resolveNamespace(ownNamespace, ref.Namespace)}, &appAuth) {
		return nil, false
	}
	return &appAuth, true
}

func getUserAuthentication(ctx xhdl.Context, cl client.Client, ownNamespace string, ref panoptikumv1alpha1.NamespacedObjectReference) (*panoptikumv1alpha1.UserAuthentication, bool) {
	var userAuth panoptikumv1alpha1.UserAuthentication
	if !apicall.ApiTryGet(ctx, cl, client.ObjectKey{Name: ref.Name, Namespace: resolveNamespace(ownNamespace, ref.Namespace)}, &userAuth) {
		return nil, false
	}
	return &userAuth, true
}
