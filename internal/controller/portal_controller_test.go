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
				Host:                  "portal.example.com",
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
			Reason:  "Test",
			Message: "Test",
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
})
