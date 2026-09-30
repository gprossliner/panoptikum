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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	panoptikumv1alpha1 "github.com/gprossliner/panoptikum/api/v1alpha1"
)

var _ = Describe("UserAuthentication Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		userauthentication := &panoptikumv1alpha1.UserAuthentication{}

		BeforeEach(func() {
			By("creating the Secret referenced by clientSecretRef/cookieSecretRef")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-oidc-secret",
					Namespace: resourceNamespace,
				},
				Data: map[string][]byte{
					"client-secret": []byte("s3cr3t"),
					"cookie-secret": []byte("c00k1e"),
				},
			}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, &corev1.Secret{})
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			}

			By("creating the custom resource for the Kind UserAuthentication")
			err = k8sClient.Get(ctx, typeNamespacedName, userauthentication)
			if err != nil && errors.IsNotFound(err) {
				resource := &panoptikumv1alpha1.UserAuthentication{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: panoptikumv1alpha1.UserAuthenticationSpec{
						IssuerURL: "https://keycloak.example.com/realms/example",
						ClientID:  "management-portal",
						ClientSecretRef: corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name},
							Key:                  "client-secret",
						},
						CookieSecretRef: corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name},
							Key:                  "cookie-secret",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &panoptikumv1alpha1.UserAuthentication{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance UserAuthentication")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-oidc-secret", Namespace: resourceNamespace}, secret)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		It("should set Ready=True when both secret keys resolve", func() {
			By("Reconciling the created resource")
			controllerReconciler := &UserAuthenticationReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			var updated panoptikumv1alpha1.UserAuthentication
			Expect(k8sClient.Get(ctx, typeNamespacedName, &updated)).To(Succeed())

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.ObservedGeneration).To(Equal(updated.Generation))
		})

		It("should set Ready=False with reason SecretKeyNotFound when a referenced key is missing", func() {
			var toUpdate panoptikumv1alpha1.UserAuthentication
			Expect(k8sClient.Get(ctx, typeNamespacedName, &toUpdate)).To(Succeed())
			toUpdate.Spec.ClientSecretRef.Key = "does-not-exist"
			Expect(k8sClient.Update(ctx, &toUpdate)).To(Succeed())

			controllerReconciler := &UserAuthenticationReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			var updated panoptikumv1alpha1.UserAuthentication
			Expect(k8sClient.Get(ctx, typeNamespacedName, &updated)).To(Succeed())

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("SecretKeyNotFound"))
		})
	})
})
