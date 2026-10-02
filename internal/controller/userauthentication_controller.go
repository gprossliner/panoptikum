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

package controller

import (
	"context"
	"fmt"

	"github.com/gprossliner/xhdl"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	panoptikumv1alpha1 "github.com/gprossliner/panoptikum/api/v1alpha1"
	"github.com/gprossliner/panoptikum/internal/apicall"
)

// UserAuthenticationReconciler reconciles a UserAuthentication object
type UserAuthenticationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Field indexer keys, used to find UserAuthentications referencing a given Secret.
const (
	clientSecretRefNameIndex = "spec.oidc.clientSecretRef.name"
	cookieSecretRefNameIndex = "spec.cookieSecretRef.name"
)

// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=userauthentications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=userauthentications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=userauthentications/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile resolves UserAuthentication.spec.oidc.clientSecretRef/cookieSecretRef
// and sets the Ready condition accordingly.
func (r *UserAuthenticationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	err := xhdl.RunContext(ctx, func(xc xhdl.Context) {
		r.reconcile(xc, req)
	})
	return ctrl.Result{}, err
}

func (r *UserAuthenticationReconciler) reconcile(ctx xhdl.Context, req ctrl.Request) {
	log := logf.FromContext(ctx)

	var userAuth panoptikumv1alpha1.UserAuthentication
	if !apicall.ApiTryGet(ctx, r.Client, req.NamespacedName, &userAuth) {
		return
	}

	ready := metav1.Condition{
		Type:               panoptikumv1alpha1.ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: userAuth.GetGeneration(),
		Reason:             "SecretsResolved",
		Message:            "clientSecretRef and cookieSecretRef both resolved",
	}

	if userAuth.Spec.OIDC == nil {
		// Only reachable for an object stored under an older schema version
		// (pre-issue #14) and never rewritten since - CEL validation isn't
		// retroactive, so an already-stored object can still be missing
		// oidc even though type is required to be OIDC today.
		ready.Status = metav1.ConditionFalse
		ready.Reason = "OIDCConfigMissing"
		ready.Message = "spec.oidc is unset"
	} else {
		for _, ref := range []struct {
			field string
			sel   corev1.SecretKeySelector
		}{
			{"oidc.clientSecretRef", userAuth.Spec.OIDC.ClientSecretRef},
			{"cookieSecretRef", userAuth.Spec.CookieSecretRef},
		} {
			if reason, message, ok := r.resolveSecretKey(ctx, userAuth.Namespace, ref.field, ref.sel); !ok {
				ready.Status = metav1.ConditionFalse
				ready.Reason = reason
				ready.Message = message
				break
			}
		}
	}

	if apimeta.SetStatusCondition(&userAuth.Status.Conditions, ready) {
		apicall.ApiUpdateStatus(ctx, r.Client, &userAuth)
	}

	log.V(1).Info("Reconciled UserAuthentication", "ready", ready.Status)
}

// resolveSecretKey checks that the Secret referenced by sel exists in namespace
// and contains key. field identifies which spec field (clientSecretRef/
// cookieSecretRef) sel came from, for the condition message.
func (r *UserAuthenticationReconciler) resolveSecretKey(ctx xhdl.Context, namespace, field string, sel corev1.SecretKeySelector) (reason, message string, ok bool) {
	var secret corev1.Secret
	if !apicall.ApiTryGet(ctx, r.Client, client.ObjectKey{Name: sel.Name, Namespace: namespace}, &secret) {
		return "SecretNotFound", fmt.Sprintf("%s: Secret %q not found", field, sel.Name), false
	}

	if _, exists := secret.Data[sel.Key]; !exists {
		return "SecretKeyNotFound", fmt.Sprintf("%s: Secret %q has no key %q", field, sel.Name, sel.Key), false
	}

	return "", "", true
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserAuthenticationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &panoptikumv1alpha1.UserAuthentication{}, clientSecretRefNameIndex, func(obj client.Object) []string {
		ua := obj.(*panoptikumv1alpha1.UserAuthentication)
		if ua.Spec.OIDC == nil {
			// Stale object from before issue #14 (see reconcile) - no
			// clientSecretRef to index yet.
			return nil
		}
		return []string{ua.Spec.OIDC.ClientSecretRef.Name}
	}); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &panoptikumv1alpha1.UserAuthentication{}, cookieSecretRefNameIndex, func(obj client.Object) []string {
		return []string{obj.(*panoptikumv1alpha1.UserAuthentication).Spec.CookieSecretRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&panoptikumv1alpha1.UserAuthentication{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapSecretToUserAuthentications),
		).
		Named("userauthentication").
		Complete(r)
}

// mapSecretToUserAuthentications requeues every UserAuthentication in the same
// namespace as secret whose clientSecretRef or cookieSecretRef names it.
func (r *UserAuthenticationReconciler) mapSecretToUserAuthentications(ctx context.Context, secret client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)
	seen := map[types.NamespacedName]struct{}{}
	var requests []reconcile.Request

	for _, indexKey := range []string{clientSecretRefNameIndex, cookieSecretRefNameIndex} {
		var list panoptikumv1alpha1.UserAuthenticationList
		if err := r.List(ctx, &list,
			client.InNamespace(secret.GetNamespace()),
			client.MatchingFields{indexKey: secret.GetName()},
		); err != nil {
			log.Error(err, "Failed to list UserAuthentications for Secret watch", "secret", secret.GetName())
			continue
		}

		for _, ua := range list.Items {
			nn := types.NamespacedName{Name: ua.Name, Namespace: ua.Namespace}
			if _, ok := seen[nn]; ok {
				continue
			}
			seen[nn] = struct{}{}
			requests = append(requests, reconcile.Request{NamespacedName: nn})
		}
	}

	return requests
}
