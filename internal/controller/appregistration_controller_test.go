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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	panoptikumv1alpha1 "github.com/gprossliner/panoptikum/api/v1alpha1"
)

var _ = Describe("AppRegistration Controller", func() {
	const namespace = "default"

	ctx := context.Background()

	reconcileAppRegistration := func(name string) *panoptikumv1alpha1.AppRegistration {
		controllerReconciler := &AppRegistrationReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
		_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
		})
		Expect(err).NotTo(HaveOccurred())

		var updated panoptikumv1alpha1.AppRegistration
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &updated)).To(Succeed())
		return &updated
	}

	createPortal := func(name, allowedAppNamespaces string) {
		portal := &panoptikumv1alpha1.Portal{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: panoptikumv1alpha1.PortalSpec{
				Host:                  testPortalHost,
				UserAuthenticationRef: panoptikumv1alpha1.NamespacedObjectReference{Name: "some-user-authentication"},
				AllowedAppNamespaces:  allowedAppNamespaces,
			},
		}
		Expect(k8sClient.Create(ctx, portal)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, portal)).To(Succeed()) })
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

	createService := func(name string) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{Port: 80}},
			},
		}
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, svc)).To(Succeed()) })
	}

	createAppRegistration := func(name, portalName, appAuthName, serviceName string) {
		appReg := &panoptikumv1alpha1.AppRegistration{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: panoptikumv1alpha1.AppRegistrationSpec{
				PortalRef:            panoptikumv1alpha1.NamespacedObjectReference{Name: portalName},
				AppAuthenticationRef: panoptikumv1alpha1.NamespacedObjectReference{Name: appAuthName},
				Routing:              panoptikumv1alpha1.AppRegistrationRouting{PathPrefix: "/app/"},
				Backend: panoptikumv1alpha1.AppRegistrationBackend{
					Service: panoptikumv1alpha1.ServiceBackend{Name: serviceName, Port: 80},
				},
			},
		}
		Expect(k8sClient.Create(ctx, appReg)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, appReg)).To(Succeed()) })
	}

	It("sets ResolvedRefs=True and Accepted=True when everything resolves and the namespace is allowed", func() {
		createPortal("ar-portal-1", ".+")
		createAppAuthentication("ar-appauth-1")
		createService("ar-service-1")
		createAppRegistration("ar-1", "ar-portal-1", "ar-appauth-1", "ar-service-1")

		updated := reconcileAppRegistration("ar-1")

		resolvedRefs := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeResolvedRefs)
		Expect(resolvedRefs).NotTo(BeNil())
		Expect(resolvedRefs.Status).To(Equal(metav1.ConditionTrue))

		accepted := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeAccepted)
		Expect(accepted).NotTo(BeNil())
		Expect(accepted.Status).To(Equal(metav1.ConditionTrue))
		Expect(accepted.Reason).To(Equal("Accepted"))
	})

	It("sets ResolvedRefs=False and Accepted=False when the Portal does not exist", func() {
		createAppAuthentication("ar-appauth-2")
		createService("ar-service-2")
		createAppRegistration("ar-2", "does-not-exist", "ar-appauth-2", "ar-service-2")

		updated := reconcileAppRegistration("ar-2")

		resolvedRefs := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeResolvedRefs)
		Expect(resolvedRefs.Status).To(Equal(metav1.ConditionFalse))
		Expect(resolvedRefs.Reason).To(Equal("PortalNotFound"))

		accepted := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeAccepted)
		Expect(accepted.Status).To(Equal(metav1.ConditionFalse))
		Expect(accepted.Reason).To(Equal("PortalNotFound"))
	})

	It("sets Accepted=False with reason NamespaceNotAllowed when the namespace doesn't match allowedAppNamespaces", func() {
		createPortal("ar-portal-3", "some-other-namespace")
		createAppAuthentication("ar-appauth-3")
		createService("ar-service-3")
		createAppRegistration("ar-3", "ar-portal-3", "ar-appauth-3", "ar-service-3")

		updated := reconcileAppRegistration("ar-3")

		resolvedRefs := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeResolvedRefs)
		Expect(resolvedRefs.Status).To(Equal(metav1.ConditionTrue))

		accepted := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeAccepted)
		Expect(accepted.Status).To(Equal(metav1.ConditionFalse))
		Expect(accepted.Reason).To(Equal("NamespaceNotAllowed"))
	})

	It("sets Accepted=Unknown when the Portal's allowedAppNamespaces regex fails to compile", func() {
		createPortal("ar-portal-4", "(")
		createAppAuthentication("ar-appauth-4")
		createService("ar-service-4")
		createAppRegistration("ar-4", "ar-portal-4", "ar-appauth-4", "ar-service-4")

		updated := reconcileAppRegistration("ar-4")

		accepted := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeAccepted)
		Expect(accepted).NotTo(BeNil())
		Expect(accepted.Status).To(Equal(metav1.ConditionUnknown))
		Expect(accepted.Reason).To(Equal("PortalAllowedNamespacesInvalid"))
	})

	It("rejects a pathPrefix starting with the reserved /_panoptikum/ prefix", func() {
		appReg := &panoptikumv1alpha1.AppRegistration{
			ObjectMeta: metav1.ObjectMeta{Name: "ar-reserved", Namespace: namespace},
			Spec: panoptikumv1alpha1.AppRegistrationSpec{
				PortalRef:            panoptikumv1alpha1.NamespacedObjectReference{Name: "ar-portal-5"},
				AppAuthenticationRef: panoptikumv1alpha1.NamespacedObjectReference{Name: "ar-appauth-5"},
				Routing:              panoptikumv1alpha1.AppRegistrationRouting{PathPrefix: "/_panoptikum/login/"},
				Backend: panoptikumv1alpha1.AppRegistrationBackend{
					Service: panoptikumv1alpha1.ServiceBackend{Name: "ar-service-5", Port: 80},
				},
			},
		}
		Expect(k8sClient.Create(ctx, appReg)).NotTo(Succeed())
	})
})
