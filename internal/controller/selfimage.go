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
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// managerContainerName is the container name used for the operator itself
// in config/manager/manager.yaml.
const managerContainerName = "manager"

// portalServerContainerName is the container name used for the generated
// portal-server Deployment (see writeDeployment in portal_controller.go).
const portalServerContainerName = "server"

// serviceAccountNamespaceFile is mounted into any pod with a service
// account token - i.e. any pod except the portal-server, per Decision 4.
const serviceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// +kubebuilder:rbac:groups="",resources=pods,verbs=get

// OwnImage returns the container image the current process is running
// under, by reading the operator's own Pod object live from the API
// server (see docs/ARCHITECTURE.md Decision 10). This is what the
// generated portal-server Deployment uses, so it's always exactly the
// same image as the operator itself, never a separately configured value.
//
// This only works when actually running as a Pod in-cluster - it
// deliberately returns an error rather than a fallback when run via
// `make run-operator` on a developer's host, where there is no "own pod"
// to look up. Takes a client.Reader (not a full client.Client) so callers
// can pass mgr.GetAPIReader() - a live, uncached read usable before the
// manager's cache has synced.
func OwnImage(ctx context.Context, cl client.Reader) (string, error) {
	namespaceBytes, err := os.ReadFile(serviceAccountNamespaceFile)
	if err != nil {
		return "", fmt.Errorf("reading own namespace from %s: %w", serviceAccountNamespaceFile, err)
	}
	namespace := strings.TrimSpace(string(namespaceBytes))

	podName, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("reading own pod name: %w", err)
	}

	var pod corev1.Pod
	if err := cl.Get(ctx, client.ObjectKey{Name: podName, Namespace: namespace}, &pod); err != nil {
		return "", fmt.Errorf("getting own pod %s/%s: %w", namespace, podName, err)
	}

	for _, c := range pod.Spec.Containers {
		if c.Name == managerContainerName {
			return c.Image, nil
		}
	}

	return "", fmt.Errorf("own pod %s/%s has no container named %q", namespace, podName, managerContainerName)
}
