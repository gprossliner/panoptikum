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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	panoptikumv1alpha1 "github.com/gprossliner/panoptikum/api/v1alpha1"
	"github.com/gprossliner/panoptikum/internal/portalconfig"
)

// testConditionReason is a placeholder Reason/Message used when directly
// setting a condition that's normally computed by another reconciler.
const testConditionReason = "Test"

var _ = Describe("Portal Controller", func() {
	const namespace = "default"

	ctx := context.Background()

	reconcilePortal := func(name string) *panoptikumv1alpha1.Portal {
		controllerReconciler := &PortalReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
		_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
		})
		Expect(err).NotTo(HaveOccurred())

		var updated panoptikumv1alpha1.Portal
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &updated)).To(Succeed())
		return &updated
	}

	createPortal := func(name, userAuthName string) {
		portal := &panoptikumv1alpha1.Portal{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: panoptikumv1alpha1.PortalSpec{
				Host:                  testPortalHost,
				UserAuthenticationRef: panoptikumv1alpha1.NamespacedObjectReference{Name: userAuthName},
			},
		}
		Expect(k8sClient.Create(ctx, portal)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, portal)).To(Succeed()) })
	}

	createUserAuthentication := func(name string) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-secret", Namespace: namespace},
			Data: map[string][]byte{
				clientSecretKey: []byte("s3cr3t"),
				cookieSecretKey: []byte("c00k1e"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, secret)).To(Succeed()) })

		userAuth := &panoptikumv1alpha1.UserAuthentication{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: panoptikumv1alpha1.UserAuthenticationSpec{
				IssuerURL: "https://issuer.example.com",
				ClientID:  "test-client",
				ClientSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name},
					Key:                  clientSecretKey,
				},
				CookieSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name},
					Key:                  cookieSecretKey,
				},
			},
		}
		Expect(k8sClient.Create(ctx, userAuth)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, userAuth)).To(Succeed()) })
	}

	createAppAuthentication := func(name string) {
		appAuth := &panoptikumv1alpha1.AppAuthentication{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: panoptikumv1alpha1.AppAuthenticationSpec{
				Type: panoptikumv1alpha1.AppAuthenticationTypeProxyAuthentication,
				ProxyAuthentication: &panoptikumv1alpha1.ProxyAuthenticationConfig{
					Headers: map[string]string{testUserHeaderName: userHeaderTemplate},
				},
			},
		}
		Expect(k8sClient.Create(ctx, appAuth)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, appAuth)).To(Succeed()) })
	}

	// createAppRegistration creates an AppRegistration referencing portalName
	// and directly sets its Accepted condition, standing in for what
	// AppRegistrationReconciler would normally compute.
	createAppRegistration := func(name, portalName string, accepted bool) {
		appReg := &panoptikumv1alpha1.AppRegistration{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: panoptikumv1alpha1.AppRegistrationSpec{
				PortalRef:            panoptikumv1alpha1.NamespacedObjectReference{Name: portalName},
				AppAuthenticationRef: panoptikumv1alpha1.NamespacedObjectReference{Name: "some-app-authentication"},
				Routing:              panoptikumv1alpha1.AppRegistrationRouting{PathPrefix: "/app/"},
				Backend: panoptikumv1alpha1.AppRegistrationBackend{
					Service: panoptikumv1alpha1.ServiceBackend{Name: "some-service", Port: 80},
				},
			},
		}
		Expect(k8sClient.Create(ctx, appReg)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, appReg)).To(Succeed()) })

		status := metav1.ConditionFalse
		if accepted {
			status = metav1.ConditionTrue
		}
		apimeta.SetStatusCondition(&appReg.Status.Conditions, metav1.Condition{
			Type:    panoptikumv1alpha1.ConditionTypeAccepted,
			Status:  status,
			Reason:  testConditionReason,
			Message: testConditionReason,
		})
		Expect(k8sClient.Status().Update(ctx, appReg)).To(Succeed())
	}

	It("sets ResolvedRefs=True and Ready=True when userAuthenticationRef resolves", func() {
		createUserAuthentication("p-userauth-1")
		createPortal("p-1", "p-userauth-1")

		updated := reconcilePortal("p-1")

		resolvedRefs := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeResolvedRefs)
		Expect(resolvedRefs).NotTo(BeNil())
		Expect(resolvedRefs.Status).To(Equal(metav1.ConditionTrue))

		ready := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeReady)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
	})

	It("sets ResolvedRefs=False and Ready=False when the UserAuthentication does not exist", func() {
		createPortal("p-2", "does-not-exist")

		updated := reconcilePortal("p-2")

		resolvedRefs := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeResolvedRefs)
		Expect(resolvedRefs.Status).To(Equal(metav1.ConditionFalse))
		Expect(resolvedRefs.Reason).To(Equal("UserAuthenticationNotFound"))

		ready := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeReady)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
	})

	It("only lists Accepted AppRegistrations in status.appRegistrations, sorted by name", func() {
		createUserAuthentication("p-userauth-3")
		createPortal("p-3", "p-userauth-3")
		createAppRegistration("p-3-app-b", "p-3", true)
		createAppRegistration("p-3-app-a", "p-3", true)
		createAppRegistration("p-3-app-not-accepted", "p-3", false)
		createAppRegistration("p-3-app-other-portal", "some-other-portal", true)

		updated := reconcilePortal("p-3")

		names := make([]string, len(updated.Status.AppRegistrations))
		for i, ref := range updated.Status.AppRegistrations {
			names[i] = ref.Name
		}
		Expect(names).To(Equal([]string{"p-3-app-a", "p-3-app-b"}))
	})

	It("writes the merged config Secret with a bound, fully-resolved app", func() {
		createUserAuthentication("p-userauth-5")
		createPortal("p-5", "p-userauth-5")
		createAppAuthentication("p-5-appauth")

		appReg := &panoptikumv1alpha1.AppRegistration{
			ObjectMeta: metav1.ObjectMeta{Name: "p-5-app", Namespace: namespace},
			Spec: panoptikumv1alpha1.AppRegistrationSpec{
				PortalRef:            panoptikumv1alpha1.NamespacedObjectReference{Name: "p-5"},
				AppAuthenticationRef: panoptikumv1alpha1.NamespacedObjectReference{Name: "p-5-appauth"},
				DisplayName:          "Grafana",
				Routing:              panoptikumv1alpha1.AppRegistrationRouting{PathPrefix: "/grafana/"},
				SortOrder:            10,
				Backend: panoptikumv1alpha1.AppRegistrationBackend{
					Service: panoptikumv1alpha1.ServiceBackend{Name: "grafana", Port: 80},
				},
			},
		}
		Expect(k8sClient.Create(ctx, appReg)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, appReg)).To(Succeed()) })

		apimeta.SetStatusCondition(&appReg.Status.Conditions, metav1.Condition{
			Type:    panoptikumv1alpha1.ConditionTypeAccepted,
			Status:  metav1.ConditionTrue,
			Reason:  testConditionReason,
			Message: testConditionReason,
		})
		Expect(k8sClient.Status().Update(ctx, appReg)).To(Succeed())

		reconcilePortal("p-5")

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "p-5-config", Namespace: namespace}, &secret)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, &secret)).To(Succeed()) })

		var cfg portalconfig.Config
		Expect(json.Unmarshal(secret.Data["config.json"], &cfg)).To(Succeed())

		Expect(cfg.Portal.Host).To(Equal(testPortalHost))
		Expect(cfg.UserAuthentication.ClientSecret).To(Equal("s3cr3t"))
		Expect(cfg.UserAuthentication.CookieSecret).To(Equal("c00k1e"))

		Expect(cfg.Apps).To(HaveLen(1))
		app := cfg.Apps[0]
		Expect(app.DisplayName).To(Equal("Grafana"))
		Expect(app.PathPrefix).To(Equal("/grafana/"))
		Expect(app.BackendURL).To(Equal("http://grafana." + namespace + ".svc.cluster.local:80"))
		Expect(app.Authorization.Type).To(Equal(portalconfig.AppAuthorizationTypeProxyAuthentication))
		Expect(app.Authorization.ProxyAuthentication).NotTo(BeNil())
		Expect(app.Authorization.ProxyAuthentication.Headers).To(Equal(map[string]string{testUserHeaderName: userHeaderTemplate}))

		Expect(secret.Annotations).To(HaveKey("panoptikum.dev/config-hash"))
	})

	It("creates the portal-server Deployment and Service once the image is known", func() {
		const portalName = "p-6"
		createUserAuthentication("p-userauth-6")
		createPortal(portalName, "p-userauth-6")

		controllerReconciler := &PortalReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			Image:  testServerImage,
		}
		_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: portalName, Namespace: namespace},
		})
		Expect(err).NotTo(HaveOccurred())

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: portalName + "-config", Namespace: namespace}, &secret)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, &secret)).To(Succeed()) })
		configHash := secret.Annotations["panoptikum.dev/config-hash"]
		Expect(configHash).NotTo(BeEmpty())

		var deployment appsv1.Deployment
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: portalName, Namespace: namespace}, &deployment)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, &deployment)).To(Succeed()) })

		Expect(*deployment.Spec.Replicas).To(Equal(int32(1)))
		Expect(deployment.Spec.Template.Annotations["panoptikum.dev/config-hash"]).To(Equal(configHash))
		Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
		container := deployment.Spec.Template.Spec.Containers[0]
		Expect(container.Image).To(Equal(testServerImage))
		Expect(container.Command).To(Equal([]string{"/server"}))
		Expect(container.Resources.Requests.Cpu().String()).To(Equal("10m"))
		Expect(*deployment.Spec.Template.Spec.AutomountServiceAccountToken).To(BeFalse())

		var service corev1.Service
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: portalName, Namespace: namespace}, &service)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, &service)).To(Succeed()) })
		Expect(service.Spec.Selector).To(Equal(map[string]string{
			"app.kubernetes.io/name":     "portal-server",
			"app.kubernetes.io/instance": portalName,
		}))
	})

	It("creates an Ingress with TLS when spec.ingress is enabled", func() {
		const portalName = "p-7"
		createUserAuthentication("p-userauth-7")

		portal := &panoptikumv1alpha1.Portal{
			ObjectMeta: metav1.ObjectMeta{Name: portalName, Namespace: namespace},
			Spec: panoptikumv1alpha1.PortalSpec{
				Host:                  testPortalHost,
				UserAuthenticationRef: panoptikumv1alpha1.NamespacedObjectReference{Name: "p-userauth-7"},
				Ingress: &panoptikumv1alpha1.PortalIngress{
					Enabled:          true,
					IngressClassName: "traefik",
					TLS:              &panoptikumv1alpha1.PortalIngressTLS{ClusterIssuer: "letsencrypt-production"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, portal)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, portal)).To(Succeed()) })

		controllerReconciler := &PortalReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			Image:  testServerImage,
		}
		_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: portalName, Namespace: namespace},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: portalName + "-config", Namespace: namespace}})).To(Succeed())
			Expect(k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: portalName, Namespace: namespace}})).To(Succeed())
			Expect(k8sClient.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: portalName, Namespace: namespace}})).To(Succeed())
		})

		var ingress networkingv1.Ingress
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: portalName, Namespace: namespace}, &ingress)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, &ingress)).To(Succeed()) })

		Expect(*ingress.Spec.IngressClassName).To(Equal("traefik"))
		Expect(ingress.Annotations["cert-manager.io/cluster-issuer"]).To(Equal("letsencrypt-production"))
		Expect(ingress.Spec.TLS).To(Equal([]networkingv1.IngressTLS{
			{Hosts: []string{testPortalHost}, SecretName: portalName + "-tls"},
		}))
		Expect(ingress.Spec.Rules).To(HaveLen(1))
		Expect(ingress.Spec.Rules[0].Host).To(Equal(testPortalHost))
		backend := ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service
		Expect(backend.Name).To(Equal(portalName))
		Expect(backend.Port.Number).To(Equal(int32(80)))
	})

	It("deletes the Ingress once spec.ingress.enabled flips back to false", func() {
		const portalName = "p-8"
		createUserAuthentication("p-userauth-8")

		portal := &panoptikumv1alpha1.Portal{
			ObjectMeta: metav1.ObjectMeta{Name: portalName, Namespace: namespace},
			Spec: panoptikumv1alpha1.PortalSpec{
				Host:                  testPortalHost,
				UserAuthenticationRef: panoptikumv1alpha1.NamespacedObjectReference{Name: "p-userauth-8"},
				Ingress:               &panoptikumv1alpha1.PortalIngress{Enabled: true},
			},
		}
		Expect(k8sClient.Create(ctx, portal)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, portal)).To(Succeed()) })
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: portalName + "-config", Namespace: namespace}})).To(Succeed())
			Expect(k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: portalName, Namespace: namespace}})).To(Succeed())
			Expect(k8sClient.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: portalName, Namespace: namespace}})).To(Succeed())
		})

		controllerReconciler := &PortalReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			Image:  testServerImage,
		}
		reconcileP8 := func() {
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: portalName, Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())
		}
		reconcileP8()

		var ingress networkingv1.Ingress
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: portalName, Namespace: namespace}, &ingress)).To(Succeed())

		var toUpdate panoptikumv1alpha1.Portal
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: portalName, Namespace: namespace}, &toUpdate)).To(Succeed())
		toUpdate.Spec.Ingress.Enabled = false
		Expect(k8sClient.Update(ctx, &toUpdate)).To(Succeed())
		reconcileP8()

		err := k8sClient.Get(ctx, types.NamespacedName{Name: portalName, Namespace: namespace}, &networkingv1.Ingress{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})
