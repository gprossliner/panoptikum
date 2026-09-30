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

	"github.com/gprossliner/xhdl"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	panoptikumv1alpha1 "github.com/gprossliner/panoptikum/api/v1alpha1"
	"github.com/gprossliner/panoptikum/internal/apicall"
)

// AppAuthenticationReconciler reconciles a AppAuthentication object
type AppAuthenticationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appauthentications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appauthentications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appauthentications/finalizers,verbs=update

// Reconcile sets the Ready condition on AppAuthentication. It has no
// outgoing references to resolve - the XValidation rules on
// AppAuthenticationSpec already guarantee a structurally valid spec by the
// time an object is persisted. The appRegistrations back-ref list is
// populated once the AppRegistration reconciler exists (see
// docs/ARCHITECTURE.md "Suggested build order").
func (r *AppAuthenticationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	err := xhdl.RunContext(ctx, func(xc xhdl.Context) {
		r.reconcile(xc, req)
	})
	return ctrl.Result{}, err
}

func (r *AppAuthenticationReconciler) reconcile(ctx xhdl.Context, req ctrl.Request) {
	log := logf.FromContext(ctx)

	var appAuth panoptikumv1alpha1.AppAuthentication
	if !apicall.ApiTryGet(ctx, r.Client, req.NamespacedName, &appAuth) {
		return
	}

	ready := metav1.Condition{
		Type:               panoptikumv1alpha1.ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: appAuth.GetGeneration(),
		Reason:             "Reconciled",
		Message:            "AppAuthentication is valid",
	}

	if apimeta.SetStatusCondition(&appAuth.Status.Conditions, ready) {
		apicall.ApiUpdateStatus(ctx, r.Client, &appAuth)
	}

	log.V(1).Info("Reconciled AppAuthentication")
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppAuthenticationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&panoptikumv1alpha1.AppAuthentication{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("appauthentication").
		Complete(r)
}
