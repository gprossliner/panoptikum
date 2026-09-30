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
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	panoptikumv1alpha1 "github.com/gprossliner/panoptikum/api/v1alpha1"
)

var _ = Describe("AppAuthentication Controller", func() {
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
		appauthentication := &panoptikumv1alpha1.AppAuthentication{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind AppAuthentication")
			err := k8sClient.Get(ctx, typeNamespacedName, appauthentication)
			if err != nil && errors.IsNotFound(err) {
				resource := &panoptikumv1alpha1.AppAuthentication{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: panoptikumv1alpha1.AppAuthenticationSpec{
						Type: panoptikumv1alpha1.AppAuthenticationTypeProxyAuthentication,
						ProxyAuthentication: &panoptikumv1alpha1.ProxyAuthenticationConfig{
							Headers: map[string]string{"X-Web-User": "$user"},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &panoptikumv1alpha1.AppAuthentication{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance AppAuthentication")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should set Ready=True", func() {
			By("Reconciling the created resource")
			controllerReconciler := &AppAuthenticationReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			var updated panoptikumv1alpha1.AppAuthentication
			Expect(k8sClient.Get(ctx, typeNamespacedName, &updated)).To(Succeed())

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, panoptikumv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.ObservedGeneration).To(Equal(updated.Generation))
		})
	})

	Context("When validating the spec", func() {
		const resourceNamespace = "default"

		ctx := context.Background()

		It("should reject a ProxyAuthentication type with no proxyAuthentication config", func() {
			resource := &panoptikumv1alpha1.AppAuthentication{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "missing-proxy-authentication",
					Namespace: resourceNamespace,
				},
				Spec: panoptikumv1alpha1.AppAuthenticationSpec{
					Type: panoptikumv1alpha1.AppAuthenticationTypeProxyAuthentication,
				},
			}
			Expect(k8sClient.Create(ctx, resource)).NotTo(Succeed())
		})

		It("should reject a header template referencing anything other than $user", func() {
			resource := &panoptikumv1alpha1.AppAuthentication{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unsupported-template-variable",
					Namespace: resourceNamespace,
				},
				Spec: panoptikumv1alpha1.AppAuthenticationSpec{
					Type: panoptikumv1alpha1.AppAuthenticationTypeProxyAuthentication,
					ProxyAuthentication: &panoptikumv1alpha1.ProxyAuthenticationConfig{
						Headers: map[string]string{"X-Web-Groups": "$groups"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).NotTo(Succeed())
		})
	})
})
