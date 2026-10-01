<!-- Hand-maintained - not generated/touched by `kubebuilder edit --plugins=helm/v2-alpha`. -->

# panoptikum

A Helm chart to distribute [panoptikum](https://github.com/gprossliner/panoptikum):
a Kubernetes operator for a single-sign-on management portal that
aggregates internal web apps behind path-based routing, with OIDC login
and per-app trusted-header authentication configured via CRDs.

See the [main repository README](https://github.com/gprossliner/panoptikum#readme)
for the full `Getting Started` walkthrough (OIDC provider setup, minimal
`Portal`/`UserAuthentication`/`AppAuthentication`/`AppRegistration`
examples) and [docs/ARCHITECTURE.md](https://github.com/gprossliner/panoptikum/blob/main/docs/ARCHITECTURE.md)
for the full design rationale. This README only covers the chart itself.

## Prerequisites

- Kubernetes 1.30+
- Helm 3.8+ (OCI support)

## Installing the chart

This chart is published as a signed OCI artifact, versioned 1:1 with each
[release](https://github.com/gprossliner/panoptikum/releases) - there's no
separate `appVersion` to track, upgrading the chart always upgrades to
that release's image.

```sh
helm install panoptikum oci://ghcr.io/gprossliner/charts/panoptikum \
  --version <version> --create-namespace --namespace panoptikum-system
```

Verify the signature (keyless [cosign](https://github.com/sigstore/cosign)):

```sh
cosign verify ghcr.io/gprossliner/charts/panoptikum:<version> \
  --certificate-identity-regexp '^https://github\.com/gprossliner/panoptikum/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Uninstalling the chart

```sh
helm uninstall panoptikum --namespace panoptikum-system
```

CRDs are kept by default (`crd.keep: true`, Helm's own behavior for
CRDs installed via a chart) - delete them manually afterwards if you
also want to remove any `Portal`/`UserAuthentication`/`AppAuthentication`/
`AppRegistration` custom resources and their CRDs.

## Configuration

Key values (see [values.yaml](values.yaml) for the full, commented list):

| Value                     | Default      | Description                                                      |
|---------------------------|--------------|-------------------------------------------------------------------|
| `manager.replicas`        | `1`          | Operator replica count.                                           |
| `manager.image.repository`| `controller` | Overridden automatically at release time - no need to set this.   |
| `manager.resources`       | see values.yaml | Operator pod resource requests/limits.                          |
| `rbac.namespaced`         | `false`      | Use namespace-scoped `Role`/`RoleBinding` instead of cluster-wide. |
| `rbac.helpers.enabled`    | `false`      | Install the 12 convenience per-CRD admin/editor/viewer `ClusterRole`s (aggregate into the standard Kubernetes `admin`/`edit`/`view` roles - see the main README). |
| `crd.enabled`             | `true`       | Install the CRDs with the chart.                                  |
| `crd.keep`                | `true`       | Keep CRDs on `helm uninstall`.                                    |
| `metrics.enabled`         | `true`       | Expose the `/metrics` endpoint.                                   |
| `metrics.secure`          | `false`      | Serve metrics over HTTPS with authn/authz (needs extra `ClusterRole` access) instead of plain HTTP. |
| `certManager.enabled`     | `false`      | Use cert-manager for webhook/metrics certificates.                |
| `webhook.enabled`         | `false`      | Enable the validating/defaulting webhook server.                  |
| `prometheus.enabled`      | `false`      | Create a `ServiceMonitor` (requires prometheus-operator CRDs).     |
| `networkPolicy.enabled`   | `false`      | Restrict ingress traffic to the controller manager.                |

### RBAC aggregation

The 12 per-CRD admin/editor/viewer `ClusterRole`s installed when
`rbac.helpers.enabled: true` carry Kubernetes' standard
`rbac.authorization.k8s.io/aggregate-to-{admin,edit,view}: "true"` labels.
That means they merge automatically into the cluster's built-in
`admin`/`edit`/`view` `ClusterRole`s - anyone already bound to one of
those (directly or via a `ClusterRoleBinding`) gets the matching
read/write/full access to `Portal`/`UserAuthentication`/
`AppAuthentication`/`AppRegistration` for free, with no extra
`RoleBinding`s to panoptikum-specific roles required.

The operator itself never needs its image configured manually -
`manager.image.repository`/`manager.image.tag` are pinned to the matching
release's image automatically when the chart is packaged (see
`.github/workflows/release.yml` in the main repo).
