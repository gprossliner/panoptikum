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
	"cmp"
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/gprossliner/xhdl"
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

// PortalReconciler reconciles a Portal object
type PortalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Field indexer key, used to find Portals referencing a given
// UserAuthentication. Index values are "namespace/name" of the *resolved*
// reference (own namespace when the ref omits one).
const userAuthenticationRefIndex = "spec.userAuthenticationRef"

// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=portals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=portals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=portals/finalizers,verbs=update
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=userauthentications,verbs=get;list;watch
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appregistrations,verbs=get;list;watch

// Reconcile resolves Portal.spec.userAuthenticationRef and computes
// status.appRegistrations from the AppRegistrations currently Accepted
// against this Portal (see docs/ARCHITECTURE.md "Reconciliation design").
// Ready currently only reflects ResolvedRefs; it will also reflect the
// generated config Secret/Deployment once Decision 4 is implemented.
func (r *PortalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	err := xhdl.RunContext(ctx, func(xc xhdl.Context) {
		r.reconcile(xc, req)
	})
	return ctrl.Result{}, err
}

func (r *PortalReconciler) reconcile(ctx xhdl.Context, req ctrl.Request) {
	log := logf.FromContext(ctx)

	var portal panoptikumv1alpha1.Portal
	if !apicall.ApiTryGet(ctx, r.Client, req.NamespacedName, &portal) {
		return
	}

	_, userAuthFound := r.getUserAuthentication(ctx, portal.Namespace, portal.Spec.UserAuthenticationRef)

	resolvedRefs := metav1.Condition{
		Type:               panoptikumv1alpha1.ConditionTypeResolvedRefs,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: portal.GetGeneration(),
		Reason:             "ResolvedRefs",
		Message:            "userAuthenticationRef resolved",
	}
	if !userAuthFound {
		resolvedRefs.Status = metav1.ConditionFalse
		resolvedRefs.Reason = "UserAuthenticationNotFound"
		resolvedRefs.Message = fmt.Sprintf("UserAuthentication %s not found", refString(portal.Namespace, portal.Spec.UserAuthenticationRef))
	}

	ready := metav1.Condition{
		Type:               panoptikumv1alpha1.ConditionTypeReady,
		Status:             resolvedRefs.Status,
		ObservedGeneration: portal.GetGeneration(),
		Reason:             resolvedRefs.Reason,
		Message:            resolvedRefs.Message,
	}

	changed := apimeta.SetStatusCondition(&portal.Status.Conditions, resolvedRefs)
	changed = apimeta.SetStatusCondition(&portal.Status.Conditions, ready) || changed

	boundAppRegistrations := r.boundAppRegistrations(ctx, &portal)
	if !reflect.DeepEqual(portal.Status.AppRegistrations, boundAppRegistrations) {
		portal.Status.AppRegistrations = boundAppRegistrations
		changed = true
	}

	if changed {
		apicall.ApiUpdateStatus(ctx, r.Client, &portal)
	}

	log.V(1).Info("Reconciled Portal", "resolvedRefs", resolvedRefs.Status, "appRegistrations", len(boundAppRegistrations))
}

func (r *PortalReconciler) getUserAuthentication(ctx xhdl.Context, ownNamespace string, ref panoptikumv1alpha1.NamespacedObjectReference) (*panoptikumv1alpha1.UserAuthentication, bool) {
	var userAuth panoptikumv1alpha1.UserAuthentication
	if !apicall.ApiTryGet(ctx, r.Client, client.ObjectKey{Name: ref.Name, Namespace: resolveNamespace(ownNamespace, ref.Namespace)}, &userAuth) {
		return nil, false
	}
	return &userAuth, true
}

// boundAppRegistrations lists every AppRegistration bound to portal
// (resolved portalRef matches, and Accepted=True), sorted for a stable
// status diff. Uses a full List + in-memory filter rather than an indexed
// List so this also works against direct (non-cached) clients, e.g. in
// tier-2 envtest reconciler tests.
func (r *PortalReconciler) boundAppRegistrations(ctx xhdl.Context, portal *panoptikumv1alpha1.Portal) []panoptikumv1alpha1.NamespacedObjectReference {
	var list panoptikumv1alpha1.AppRegistrationList
	apicall.ApiList(ctx, r.Client, &list)

	var bound []panoptikumv1alpha1.NamespacedObjectReference
	for _, ar := range list.Items {
		if resolveNamespace(ar.Namespace, ar.Spec.PortalRef.Namespace) != portal.Namespace || ar.Spec.PortalRef.Name != portal.Name {
			continue
		}
		if !apimeta.IsStatusConditionTrue(ar.Status.Conditions, panoptikumv1alpha1.ConditionTypeAccepted) {
			continue
		}
		bound = append(bound, panoptikumv1alpha1.NamespacedObjectReference{Name: ar.Name, Namespace: ar.Namespace})
	}

	slices.SortFunc(bound, func(a, b panoptikumv1alpha1.NamespacedObjectReference) int {
		if c := cmp.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})

	return bound
}

// SetupWithManager sets up the controller with the Manager.
func (r *PortalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &panoptikumv1alpha1.Portal{}, userAuthenticationRefIndex, func(obj client.Object) []string {
		p := obj.(*panoptikumv1alpha1.Portal)
		return []string{refString(p.Namespace, p.Spec.UserAuthenticationRef)}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&panoptikumv1alpha1.Portal{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&panoptikumv1alpha1.UserAuthentication{}, handler.EnqueueRequestsFromMapFunc(r.mapUserAuthenticationToPortals)).
		Watches(&panoptikumv1alpha1.AppRegistration{}, handler.EnqueueRequestsFromMapFunc(mapAppRegistrationToPortal)).
		Named("portal").
		Complete(r)
}

// mapUserAuthenticationToPortals requeues every Portal referencing the
// changed UserAuthentication. This is a reverse lookup (the
// UserAuthentication doesn't know which Portals reference it), so it needs
// the field indexer and only ever runs via the manager's cached client.
func (r *PortalReconciler) mapUserAuthenticationToPortals(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)

	var list panoptikumv1alpha1.PortalList
	key := obj.GetNamespace() + "/" + obj.GetName()
	if err := r.List(ctx, &list, client.MatchingFields{userAuthenticationRefIndex: key}); err != nil {
		log.Error(err, "Failed to list Portals for UserAuthentication watch", "key", key)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, p := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: p.Name, Namespace: p.Namespace}})
	}
	return requests
}

// mapAppRegistrationToPortal requeues the Portal a changed AppRegistration
// references. This is a forward lookup (the AppRegistration already names
// its own Portal), so no List/index is needed.
func mapAppRegistrationToPortal(_ context.Context, obj client.Object) []reconcile.Request {
	ar, ok := obj.(*panoptikumv1alpha1.AppRegistration)
	if !ok {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Name:      ar.Spec.PortalRef.Name,
		Namespace: resolveNamespace(ar.Namespace, ar.Spec.PortalRef.Namespace),
	}}}
}
