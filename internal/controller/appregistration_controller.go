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
	"regexp"

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

// AppRegistrationReconciler reconciles a AppRegistration object
type AppRegistrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Field indexer keys, used to find AppRegistrations referencing a given
// Portal/AppAuthentication/backend Service. Index values are
// "namespace/name" of the *resolved* reference (own namespace when the ref
// omits one).
const (
	portalRefIndex            = "spec.portalRef"
	appAuthenticationRefIndex = "spec.appAuthenticationRef"
	backendServiceRefIndex    = "spec.backend.service"
)

// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appregistrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appregistrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appregistrations/finalizers,verbs=update
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=portals,verbs=get;list;watch
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appauthentications,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch

// Reconcile resolves AppRegistration.spec.portalRef/appAuthenticationRef/
// backend.service and sets the ResolvedRefs and Accepted conditions
// accordingly (see docs/ARCHITECTURE.md "Reconciliation design").
func (r *AppRegistrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	err := xhdl.RunContext(ctx, func(xc xhdl.Context) {
		r.reconcile(xc, req)
	})
	return ctrl.Result{}, err
}

func (r *AppRegistrationReconciler) reconcile(ctx xhdl.Context, req ctrl.Request) {
	log := logf.FromContext(ctx)

	var appReg panoptikumv1alpha1.AppRegistration
	if !apicall.ApiTryGet(ctx, r.Client, req.NamespacedName, &appReg) {
		return
	}

	portal, portalFound := getPortal(ctx, r.Client, appReg.Namespace, appReg.Spec.PortalRef)
	_, appAuthFound := getAppAuthentication(ctx, r.Client, appReg.Namespace, appReg.Spec.AppAuthenticationRef)
	_, serviceFound := r.getBackendService(ctx, appReg.Namespace, appReg.Spec.Backend.Service)

	resolvedRefs := metav1.Condition{
		Type:               panoptikumv1alpha1.ConditionTypeResolvedRefs,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: appReg.GetGeneration(),
		Reason:             "ResolvedRefs",
		Message:            "portalRef, appAuthenticationRef and backend.service all resolved",
	}
	switch {
	case !portalFound:
		resolvedRefs.Status = metav1.ConditionFalse
		resolvedRefs.Reason = "PortalNotFound"
		resolvedRefs.Message = fmt.Sprintf("Portal %s not found", refString(appReg.Namespace, appReg.Spec.PortalRef))
	case !appAuthFound:
		resolvedRefs.Status = metav1.ConditionFalse
		resolvedRefs.Reason = "AppAuthenticationNotFound"
		resolvedRefs.Message = fmt.Sprintf("AppAuthentication %s not found", refString(appReg.Namespace, appReg.Spec.AppAuthenticationRef))
	case !serviceFound:
		resolvedRefs.Status = metav1.ConditionFalse
		resolvedRefs.Reason = "ServiceNotFound"
		resolvedRefs.Message = fmt.Sprintf("Service %s not found", serviceRefString(appReg.Namespace, appReg.Spec.Backend.Service))
	}

	accepted := evaluateAccepted(appReg.Namespace, appReg.GetGeneration(), portal, portalFound, appReg.Spec.Routes)

	changed := apimeta.SetStatusCondition(&appReg.Status.Conditions, resolvedRefs)
	changed = apimeta.SetStatusCondition(&appReg.Status.Conditions, accepted) || changed

	if changed {
		apicall.ApiUpdateStatus(ctx, r.Client, &appReg)
	}

	log.V(1).Info("Reconciled AppRegistration", "resolvedRefs", resolvedRefs.Status, "accepted", accepted.Status)
}

func (r *AppRegistrationReconciler) getBackendService(ctx xhdl.Context, ownNamespace string, svc panoptikumv1alpha1.ServiceBackend) (*corev1.Service, bool) {
	var service corev1.Service
	if !apicall.ApiTryGet(ctx, r.Client, client.ObjectKey{Name: svc.Name, Namespace: resolveNamespace(ownNamespace, svc.Namespace)}, &service) {
		return nil, false
	}
	return &service, true
}

// evaluateAccepted implements docs/ARCHITECTURE.md Decision 8: the Portal's
// allowedAppNamespaces regex is anchored before matching (Go's regexp is
// unanchored by default), and an invalid pattern yields Unknown rather than
// silently allowing or rejecting everything. Also validates routes[].match
// (issue #11) compiles - unanchored, unlike allowedAppNamespaces, since
// route patterns are meant to be partial (e.g. a "^/public-dashboards/"
// prefix with no trailing "$").
func evaluateAccepted(namespace string, generation int64, portal *panoptikumv1alpha1.Portal, portalFound bool, routes []panoptikumv1alpha1.AppRegistrationRoute) metav1.Condition {
	accepted := metav1.Condition{
		Type:               panoptikumv1alpha1.ConditionTypeAccepted,
		ObservedGeneration: generation,
	}

	if !portalFound {
		accepted.Status = metav1.ConditionFalse
		accepted.Reason = "PortalNotFound"
		accepted.Message = "referenced Portal not found"
		return accepted
	}

	if i, err := firstInvalidRoute(routes); err != nil {
		accepted.Status = metav1.ConditionUnknown
		accepted.Reason = "RouteMatchInvalid"
		accepted.Message = fmt.Sprintf("routes[%d].match %q does not compile: %s", i, routes[i].Match, err)
		return accepted
	}

	pattern := portal.Spec.AllowedAppNamespaces
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		accepted.Status = metav1.ConditionUnknown
		accepted.Reason = "PortalAllowedNamespacesInvalid"
		accepted.Message = fmt.Sprintf("Portal's allowedAppNamespaces %q does not compile: %s", pattern, err)
		return accepted
	}

	if !re.MatchString(namespace) {
		accepted.Status = metav1.ConditionFalse
		accepted.Reason = "NamespaceNotAllowed"
		accepted.Message = fmt.Sprintf("namespace %q does not match Portal's allowedAppNamespaces %q", namespace, pattern)
		return accepted
	}

	accepted.Status = metav1.ConditionTrue
	accepted.Reason = "Accepted"
	accepted.Message = "namespace allowed by Portal's allowedAppNamespaces"
	return accepted
}

// firstInvalidRoute returns the index and compile error of the first route
// whose match pattern fails to compile, or err=nil if every route (or an
// empty/nil list) compiles.
func firstInvalidRoute(routes []panoptikumv1alpha1.AppRegistrationRoute) (index int, err error) {
	for i, route := range routes {
		if _, err := regexp.Compile(route.Match); err != nil {
			return i, err
		}
	}
	return -1, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppRegistrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	indexers := []struct {
		key string
		fn  client.IndexerFunc
	}{
		{portalRefIndex, func(obj client.Object) []string {
			ar := obj.(*panoptikumv1alpha1.AppRegistration)
			return []string{refString(ar.Namespace, ar.Spec.PortalRef)}
		}},
		{appAuthenticationRefIndex, func(obj client.Object) []string {
			ar := obj.(*panoptikumv1alpha1.AppRegistration)
			return []string{refString(ar.Namespace, ar.Spec.AppAuthenticationRef)}
		}},
		{backendServiceRefIndex, func(obj client.Object) []string {
			ar := obj.(*panoptikumv1alpha1.AppRegistration)
			return []string{serviceRefString(ar.Namespace, ar.Spec.Backend.Service)}
		}},
	}

	for _, idx := range indexers {
		if err := mgr.GetFieldIndexer().IndexField(context.Background(), &panoptikumv1alpha1.AppRegistration{}, idx.key, idx.fn); err != nil {
			return err
		}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&panoptikumv1alpha1.AppRegistration{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&panoptikumv1alpha1.Portal{}, handler.EnqueueRequestsFromMapFunc(r.mapIndexed(portalRefIndex))).
		Watches(&panoptikumv1alpha1.AppAuthentication{}, handler.EnqueueRequestsFromMapFunc(r.mapIndexed(appAuthenticationRefIndex))).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(r.mapIndexed(backendServiceRefIndex))).
		Named("appregistration").
		Complete(r)
}

// mapIndexed returns a handler.MapFunc that requeues every AppRegistration
// whose indexKey field matches the changed object's namespace/name.
func (r *AppRegistrationReconciler) mapIndexed(indexKey string) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := logf.FromContext(ctx)

		var list panoptikumv1alpha1.AppRegistrationList
		key := obj.GetNamespace() + "/" + obj.GetName()
		if err := r.List(ctx, &list, client.MatchingFields{indexKey: key}); err != nil {
			log.Error(err, "Failed to list AppRegistrations for watch", "indexKey", indexKey, "key", key)
			return nil
		}

		requests := make([]reconcile.Request, 0, len(list.Items))
		for _, ar := range list.Items {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: ar.Name, Namespace: ar.Namespace}})
		}
		return requests
	}
}
