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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/gprossliner/xhdl"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	panoptikumv1alpha1 "github.com/gprossliner/panoptikum/api/v1alpha1"
	"github.com/gprossliner/panoptikum/internal/apicall"
	"github.com/gprossliner/panoptikum/internal/portalconfig"
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

// configSecretDataKey is the key under which the merged portalconfig.Config
// JSON document is stored in the generated Secret's Data.
const configSecretDataKey = "config.json"

// configHashAnnotation records the sha256 of the last-written config JSON,
// so a future Deployment rollout (Decision 4) can detect whether it needs
// to restart pods without re-marshaling/re-hashing the config itself.
const configHashAnnotation = "panoptikum.dev/config-hash"

// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=portals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=portals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=portals/finalizers,verbs=update
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=userauthentications,verbs=get;list;watch
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appregistrations,verbs=get;list;watch
// +kubebuilder:rbac:groups=panoptikum.panoptikum.dev,resources=appauthentications,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

// Reconcile resolves Portal.spec.userAuthenticationRef, computes
// status.appRegistrations from the AppRegistrations currently Accepted
// against this Portal, and writes the merged portalconfig.Config document
// into a generated Secret (see docs/ARCHITECTURE.md Decision 4 and
// "Reconciliation design"). The generated Deployment/rollout from Decision
// 4 is not implemented yet.
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

	userAuth, userAuthFound := getUserAuthentication(ctx, r.Client, portal.Namespace, portal.Spec.UserAuthenticationRef)

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

	bound := r.boundAppRegistrations(ctx, &portal)

	ready := metav1.Condition{
		Type:               panoptikumv1alpha1.ConditionTypeReady,
		ObservedGeneration: portal.GetGeneration(),
	}
	switch {
	case !userAuthFound:
		ready.Status = metav1.ConditionFalse
		ready.Reason = resolvedRefs.Reason
		ready.Message = resolvedRefs.Message
	default:
		if cfg, ok := r.buildConfig(ctx, &portal, userAuth, bound); ok {
			r.writeConfigSecret(ctx, &portal, cfg)
			ready.Status = metav1.ConditionTrue
			ready.Reason = "ConfigWritten"
			ready.Message = "merged config Secret written"
		} else {
			ready.Status = metav1.ConditionFalse
			ready.Reason = "UserAuthenticationSecretUnresolved"
			ready.Message = "userAuthenticationRef's clientSecretRef/cookieSecretRef could not be resolved"
		}
	}

	changed := apimeta.SetStatusCondition(&portal.Status.Conditions, resolvedRefs)
	changed = apimeta.SetStatusCondition(&portal.Status.Conditions, ready) || changed

	appRegistrations := appRegistrationRefs(bound)
	if !reflect.DeepEqual(portal.Status.AppRegistrations, appRegistrations) {
		portal.Status.AppRegistrations = appRegistrations
		changed = true
	}

	if changed {
		apicall.ApiUpdateStatus(ctx, r.Client, &portal)
	}

	log.V(1).Info("Reconciled Portal", "resolvedRefs", resolvedRefs.Status, "ready", ready.Status, "appRegistrations", len(appRegistrations))
}

// boundAppRegistrations lists every AppRegistration bound to portal
// (resolved portalRef matches, and Accepted=True), sorted by namespace/name
// for a stable order. Uses a full List + in-memory filter rather than an
// indexed List so this also works against direct (non-cached) clients,
// e.g. in tier-2 envtest reconciler tests.
func (r *PortalReconciler) boundAppRegistrations(ctx xhdl.Context, portal *panoptikumv1alpha1.Portal) []panoptikumv1alpha1.AppRegistration {
	var list panoptikumv1alpha1.AppRegistrationList
	apicall.ApiList(ctx, r.Client, &list)

	var bound []panoptikumv1alpha1.AppRegistration
	for _, ar := range list.Items {
		if resolveNamespace(ar.Namespace, ar.Spec.PortalRef.Namespace) != portal.Namespace || ar.Spec.PortalRef.Name != portal.Name {
			continue
		}
		if !apimeta.IsStatusConditionTrue(ar.Status.Conditions, panoptikumv1alpha1.ConditionTypeAccepted) {
			continue
		}
		bound = append(bound, ar)
	}

	slices.SortFunc(bound, func(a, b panoptikumv1alpha1.AppRegistration) int {
		if c := cmp.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})

	return bound
}

// appRegistrationRefs projects bound AppRegistrations down to the
// name/namespace pairs stored in Portal.status.appRegistrations. Returns nil
// (not an empty slice) when bound is empty, to match status.AppRegistrations'
// zero value for a stable reflect.DeepEqual diff.
func appRegistrationRefs(bound []panoptikumv1alpha1.AppRegistration) []panoptikumv1alpha1.NamespacedObjectReference {
	if len(bound) == 0 {
		return nil
	}
	refs := make([]panoptikumv1alpha1.NamespacedObjectReference, 0, len(bound))
	for _, ar := range bound {
		refs = append(refs, panoptikumv1alpha1.NamespacedObjectReference{Name: ar.Name, Namespace: ar.Namespace})
	}
	return refs
}

// buildConfig resolves the UserAuthentication's actual secret values and
// every bound AppRegistration's AppAuthentication, producing the document
// to write into the generated config Secret. Returns ok=false only if the
// UserAuthentication's own secret data doesn't resolve - a hard requirement,
// since the portal-server can't render anything without it. An individual
// AppRegistration whose AppAuthentication doesn't resolve is skipped
// (logged) rather than failing the whole Portal's config.
func (r *PortalReconciler) buildConfig(ctx xhdl.Context, portal *panoptikumv1alpha1.Portal, userAuth *panoptikumv1alpha1.UserAuthentication, bound []panoptikumv1alpha1.AppRegistration) (portalconfig.Config, bool) {
	log := logf.FromContext(ctx)

	clientSecret, ok := r.getSecretValue(ctx, userAuth.Namespace, userAuth.Spec.ClientSecretRef)
	if !ok {
		return portalconfig.Config{}, false
	}
	cookieSecret, ok := r.getSecretValue(ctx, userAuth.Namespace, userAuth.Spec.CookieSecretRef)
	if !ok {
		return portalconfig.Config{}, false
	}

	cfg := portalconfig.Config{
		Portal: portalconfig.PortalConfig{
			Host:        portal.Spec.Host,
			DisplayName: portal.Spec.DisplayName,
		},
		UserAuthentication: portalconfig.UserAuthenticationConfig{
			IssuerURL:            userAuth.Spec.IssuerURL,
			ClientID:             userAuth.Spec.ClientID,
			ClientSecret:         clientSecret,
			CookieSecret:         cookieSecret,
			Scopes:               userAuth.Spec.Scopes,
			AllowUnverifiedEmail: userAuth.Spec.AllowUnverifiedEmail,
		},
	}

	if portal.Spec.Customization != nil {
		cfg.Portal.Customization = &portalconfig.CustomizationConfig{
			LogoURL:         portal.Spec.Customization.LogoURL,
			FaviconURL:      portal.Spec.Customization.FaviconURL,
			BackgroundColor: portal.Spec.Customization.BackgroundColor,
			AccentColor:     portal.Spec.Customization.AccentColor,
		}
	}

	for _, ar := range bound {
		appCfg, ok := buildAppConfig(ctx, r.Client, ar)
		if !ok {
			log.Info("Skipping AppRegistration with unresolved appAuthenticationRef", "name", ar.Name, "namespace", ar.Namespace)
			continue
		}
		cfg.Apps = append(cfg.Apps, appCfg)
	}

	slices.SortFunc(cfg.Apps, func(a, b portalconfig.AppConfig) int {
		return cmp.Compare(a.SortOrder, b.SortOrder)
	})

	return cfg, true
}

// buildAppConfig resolves ar's AppAuthentication and inlines it, alongside a
// fully resolved BackendURL, into one portalconfig.AppConfig entry.
func buildAppConfig(ctx xhdl.Context, cl client.Client, ar panoptikumv1alpha1.AppRegistration) (portalconfig.AppConfig, bool) {
	appAuth, ok := getAppAuthentication(ctx, cl, ar.Namespace, ar.Spec.AppAuthenticationRef)
	if !ok {
		return portalconfig.AppConfig{}, false
	}

	svcNamespace := resolveNamespace(ar.Namespace, ar.Spec.Backend.Service.Namespace)
	backendURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", ar.Spec.Backend.Service.Name, svcNamespace, ar.Spec.Backend.Service.Port)

	authorization := portalconfig.AppAuthorization{
		Type: portalconfig.AppAuthorizationType(appAuth.Spec.Type),
	}
	if appAuth.Spec.ProxyAuthentication != nil {
		authorization.ProxyAuthentication = &portalconfig.ProxyAuthenticationConfig{
			Headers: appAuth.Spec.ProxyAuthentication.Headers,
		}
	}

	return portalconfig.AppConfig{
		DisplayName:   ar.Spec.DisplayName,
		PathPrefix:    ar.Spec.Routing.PathPrefix,
		SortOrder:     ar.Spec.SortOrder,
		BackendURL:    backendURL,
		Authorization: authorization,
	}, true
}

// getSecretValue returns the value of sel.Key in the Secret sel.Name,
// namespace, or ok=false if the Secret or key doesn't exist.
func (r *PortalReconciler) getSecretValue(ctx xhdl.Context, namespace string, sel corev1.SecretKeySelector) (string, bool) {
	var secret corev1.Secret
	if !apicall.ApiTryGet(ctx, r.Client, client.ObjectKey{Name: sel.Name, Namespace: namespace}, &secret) {
		return "", false
	}
	value, ok := secret.Data[sel.Key]
	if !ok {
		return "", false
	}
	return string(value), true
}

// writeConfigSecret creates or updates the generated config Secret for
// portal from cfg, owned via ownerReferences so it's garbage-collected with
// the Portal (see docs/ARCHITECTURE.md Decision 4).
func (r *PortalReconciler) writeConfigSecret(ctx xhdl.Context, portal *panoptikumv1alpha1.Portal, cfg portalconfig.Config) {
	data, err := json.Marshal(cfg)
	ctx.Throw(err)
	hash := sha256.Sum256(data)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      portal.Name + "-config",
			Namespace: portal.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[configSecretDataKey] = data

		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations[configHashAnnotation] = hex.EncodeToString(hash[:])

		return controllerutil.SetControllerReference(portal, secret, r.Scheme)
	})
	ctx.Throw(err)
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
		Owns(&corev1.Secret{}).
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
