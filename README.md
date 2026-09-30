# Panoptikum

A Kubernetes operator for a single-sign-on management portal: one authenticated
entry point that aggregates a set of internal web apps (Headlamp, Grafana,
Prometheus, ...) behind path-based routing, without every app needing its own
Ingress, TLS, or login.

It's a declarative, CRD-driven operator: registering a new app becomes
"apply an `AppRegistration`", authentication is configured via CRDs rather
than hand-edited proxy config, and the OIDC + reverse-proxy logic runs
natively in Go instead of depending on nginx + oauth2-proxy. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design rationale.

> **Status:** early design phase. CRD shapes below are the current working
> draft, not yet implemented. Nothing in this repo is stable API.

## Custom Resources

| Kind                 | Scope     | Purpose                                                                 |
|----------------------|-----------|--------------------------------------------------------------------------|
| `Portal`             | Namespaced | An instance of the user-visible portal shell (nav, branding, ingress host). |
| `UserAuthentication` | Namespaced | The OIDC login gating a `Portal` (the human logging into the portal).     |
| `AppAuthentication`  | Namespaced | How the portal vouches for the logged-in user to a backend app (currently `ProxyAuthentication`: inject trusted headers, e.g. `X-Web-User: $user`). |
| `AppRegistration`    | Namespaced | One app entry in a `Portal`'s nav: displayName, path prefix, backend `Service`, sort order, and which `AppAuthentication` to use. |

A `Portal` is typically singular per cluster, but multiple `Portal`s are
supported (e.g. per team/environment). References between kinds may cross
namespaces (e.g. an app team's `AppRegistration` pointing at a shared
`Portal` in another namespace).

## Non-goals (initial release)

- **Only `$user` is passed to apps** — no groups/roles/other claims via
  `AppAuthentication` headers. Same restriction as the predecessor
  Terraform module.
- **Apps must support being served under a path prefix** (e.g.
  `grafana.svc.ns.cluster.local/grafana/`), not only at their origin's
  root. Same restriction as the predecessor Terraform module.

## Example

```yaml
apiVersion: panoptikum.dev/v1alpha1
kind: UserAuthentication
metadata:
  name: keycloak
  namespace: management-portal
spec:
  issuerURL: https://keycloak.example.com/realms/example
  clientID: management-portal
  clientSecretRef:
    name: keycloak-client
    key: client-secret
  cookieSecretRef:
    name: keycloak-client
    key: cookie-secret
---
apiVersion: panoptikum.dev/v1alpha1
kind: Portal
metadata:
  name: main
  namespace: management-portal
spec:
  host: portal.example.com
  displayName: Management Portal
  customization:
    logoURL: https://example.com/logo.svg
    faviconURL: https://example.com/favicon.ico
    backgroundColor: "#303030"
    accentColor: "#4f8cff"
  userAuthenticationRef:
    name: keycloak
  allowedAppNamespaces: "management-portal|team-a-.+"
  ingress:
    enabled: true
    ingressClassName: traefik
    tls:
      clusterIssuer: letsencrypt-production
---
apiVersion: panoptikum.dev/v1alpha1
kind: AppAuthentication
metadata:
  name: proxy-auth
  namespace: management-portal
spec:
  type: ProxyAuthentication
  proxyAuthentication:
    headers:
      X-Web-User: $user
---
apiVersion: panoptikum.dev/v1alpha1
kind: AppRegistration
metadata:
  name: grafana
  namespace: management-portal
spec:
  portalRef:
    name: main
  appAuthenticationRef:
    name: proxy-auth
  displayName: Grafana
  routing:
    pathPrefix: /grafana/
  sortOrder: 40
  backend:
    service:
      name: grafana
      port: 80
```

## Status conventions

- Resources that reference another resource (e.g. `AppRegistration` ->
  `Portal`) carry an `Accepted` condition once the target resource has bound
  them.
- Resources that are referenced by others (`Portal`, `UserAuthentication`,
  `AppAuthentication`) carry a status list of the resources currently bound
  to them, e.g. `portal.status.appRegistrations[]`.

Details in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), Decision 5.

## Installation

Not yet available. A Helm chart is planned, which will also optionally
bootstrap a "default" `Portal` + pre-provisioned `Secret`. Exact bootstrap
semantics are still to be decided.
