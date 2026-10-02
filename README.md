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

> **Status:** early releases. CRD shapes are implemented and usable, but
> not yet guaranteed stable API - expect breaking changes before v1.

## Getting Started

Minimum steps to get a working portal with one app (here: Grafana,
assumed already deployed in the same namespace behind a `Service` named
`grafana` on port `80`) registered. See [Known Apps](#known-apps) below
for the Grafana Helm values this example assumes (sub-path routing,
disabled `Ingress`, trusted-header auth).

1. Install the Helm chart (published as an OCI artifact, signed with
   [cosign](https://github.com/sigstore/cosign)):

   ```sh
   helm install panoptikum oci://ghcr.io/gprossliner/charts/panoptikum \
     --version <version> --create-namespace --namespace panoptikum-system
   ```

   See [Releases](https://github.com/gprossliner/panoptikum/releases) for
   available versions.

2. Create a `Secret` holding your OIDC client secret and a cookie-signing key:

   ```sh
   kubectl create secret generic keycloak-client \
     --from-literal=client-secret=<your-oidc-client-secret> \
     --from-literal=cookie-secret="$(openssl rand -base64 32)"
   ```

3. Apply the minimal CRs below - only `+required` fields are shown,
   everything else (ingress, branding, replica count, ...) has a sensible
   default; see [Custom Resources](#custom-resources) below for the full shape:

   ```yaml
   apiVersion: panoptikum.panoptikum.dev/v1alpha1
   kind: UserAuthentication
   metadata:
     name: keycloak
   spec:
     type: OIDC
     cookieSecretRef:
       name: keycloak-client
       key: cookie-secret
     oidc:
       issuerURL: https://keycloak.example.com/realms/example
       clientID: management-portal
       clientSecretRef:
         name: keycloak-client
         key: client-secret
   ---
   apiVersion: panoptikum.panoptikum.dev/v1alpha1
   kind: Portal
   metadata:
     name: main
   spec:
     host: panoptikum.example.com
     userAuthenticationRef:
       name: keycloak
     ingress:
       enabled: true
       ingressClassName: nginx
       tls:
         clusterIssuer: letsencrypt-prod
   ---
   apiVersion: panoptikum.panoptikum.dev/v1alpha1
   kind: AppAuthentication
   metadata:
     name: grafana
   spec:
     type: ProxyAuthentication
     proxyAuthentication:
       headers:
         X-Web-User: $user
   ---
   apiVersion: panoptikum.panoptikum.dev/v1alpha1
   kind: AppRegistration
   metadata:
     name: grafana
   spec:
     portalRef:
       name: main
     appAuthenticationRef:
       name: grafana
     routing:
       pathPrefix: /grafana/
     backend:
       service:
         name: grafana
         port: 80
   ```

   > Adjust `ingress.ingressClassName` to whatever Ingress controller your
   > cluster runs, and `ingress.tls.clusterIssuer` to a cert-manager
   > `ClusterIssuer` you have configured (or drop `tls` entirely to serve
   > plain HTTP).

## OIDC Configuration

The redirect URI panoptikum uses isn't configured directly - it's always
derived from the owning `Portal`'s `spec.host`, as
`https://<host>/_panoptikum/oidc-callback`. For the `panoptikum.example.com`
example above, register these with your OIDC provider (Keycloak, Dex,
Okta, ...) when creating the client:

| Setting              | Value                                                     |
|-----------------------|------------------------------------------------------------|
| Redirect URI          | `https://panoptikum.example.com/_panoptikum/oidc-callback` |
| Client ID             | Whatever you set as `UserAuthentication.spec.oidc.clientID` |
| Client secret         | Stored in the `Secret` referenced by `clientSecretRef`      |
| Scopes                | `openid profile email` (the default; override via `spec.oidc.scopes`) |

Two distinct secrets are involved, both read from `Secret`s you create
yourself (never generated or stored by the operator) - easy to conflate
since the Getting Started example above puts both in the same `Secret`:

- **`clientSecretRef`** - the OIDC client secret issued by your identity
  provider when you register panoptikum as a client.
- **`cookieSecretRef`** - *not* related to the identity provider at all.
  It's a symmetric key panoptikum generates and uses itself, to
  encrypt/sign session and OIDC-handshake cookies (AES-256-GCM, see
  [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) Decision 7). Generate one
  with `openssl rand -base64 32` - it never needs to match anything on
  the identity-provider side.

`issuerURL` must exactly match the issuer your provider reports at its
`/.well-known/openid-configuration` discovery document - used both to
discover the token/authorization endpoints and to validate ID tokens.

## Expected Outcome

Once the CRs above are `Accepted` (see [Status conventions](#status-conventions)
below), the operator has reconciled everything needed to actually reach
the portal:

- A `Deployment`, config `Secret`, `Service`, and (if `ingress.enabled`)
  `Ingress` for the portal-server, created in the `Portal`'s own namespace.
- The `Deployment` uses the correct portal-server image automatically -
  nothing to configure yourself (see docs/ARCHITECTURE.md Decision 10).
- You can browse directly to `https://panoptikum.example.com` to open the portal.
- Grafana is reachable from inside the portal, under its registered
  `/grafana/` path prefix.

## High Availability

The generated portal-server `Deployment` is stateless aside from
session/OIDC-handshake cookies, which every replica can decrypt/verify
using the same shared key (`cookieSecretRef`, see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
Decision 7) - so running multiple replicas behind the generated `Service`
needs no sticky sessions or shared server-side store. Set
`Portal.spec.server.replicas` to more than `1` to run multiple
portal-server pods:

```yaml
spec:
  server:
    replicas: 3
```

The operator and portal-server are also independently resilient to each
other: the portal-server never talks to the Kubernetes API (Decision 4),
so if the operator is down or crash-looping, already-running
portal-server pods keep serving traffic unaffected, using the last
successfully-written config `Secret` - they just won't pick up any new
`AppRegistration`/`Portal` changes until the operator recovers.

## Known Apps

Helm values known to work well behind panoptikum for a few common apps -
only the values relevant to sub-path routing, removing the app's own
`Ingress`, and trusting panoptikum's injected auth header are shown
below, not a full `values.yaml`.

### Grafana (`grafana/grafana`)

```yaml
ingress:
  enabled: false   # no Ingress of its own - only reachable via panoptikum's reverse proxy

"grafana.ini":
  server:
    # a literal external URL, not %(protocol)s/%(domain)s placeholders -
    # those reflect Grafana's own listener config (server.protocol),
    # and setting that to https would make Grafana expect its own TLS
    # cert/key, which it doesn't have here (TLS terminates at the
    # Ingress, Grafana itself only ever speaks plain HTTP). A literal
    # https root_url also fixes Grafana Live's websocket handshake,
    # which otherwise rejects the browser's Origin header with a 403.
    root_url: https://panoptikum.example.com/grafana/
    serve_from_sub_path: true
  auth.proxy:
    enabled: true
    header_name: X-Web-User
    header_property: username
    auto_sign_up: true
  # no security.allow_embedding needed - the portal rewrites Grafana's
  # default X-Frame-Options: deny to SAMEORIGIN automatically
```

Matching `AppAuthentication`:

```yaml
spec:
  type: ProxyAuthentication
  proxyAuthentication:
    headers:
      X-Web-User: $user
```

### Headlamp (`headlamp/headlamp`)

```yaml
config:
  baseURL: /headlamp
  unsafeUseServiceAccountToken: true   # skip Headlamp's own login - panoptikum's OIDC gate is the only auth
ingress:
  enabled: false
```

Headlamp has no per-user header-trust mechanism (`unsafeUseServiceAccountToken`
grants every request its own in-cluster ServiceAccount, not a per-user
identity), so its `AppAuthentication` only needs to exist to satisfy
`AppRegistration.spec.appAuthenticationRef` - the header content itself
is unused by Headlamp.

### Prometheus (`prometheus-community/kube-prometheus-stack`)

```yaml
prometheus:
  prometheusSpec:
    externalUrl: "https://panoptikum.example.com/prometheus/"
    routePrefix: /prometheus
```

No `Ingress` to disable (this chart doesn't create one by default), and no
header-based auth to configure - Prometheus has no built-in login, so
access is controlled entirely by not exposing it any other way and
relying on panoptikum's own OIDC gate in front.

### Jaeger (`jaegertracing/jaeger`)

```yaml
userconfig:
  extensions:
    jaeger_query:
      base_path: /jaeger
```

Same story as Prometheus: no `Ingress` to disable by default, and no
built-in login to delegate to a trusted header - panoptikum's OIDC gate
is the only access control.

## Security

- **Signed images and chart**: both the container image and the Helm
  chart are published as signed OCI artifacts ([cosign](https://github.com/sigstore/cosign)
  keyless/OIDC-based - no key material to manage or leak). See
  [Getting Started](#getting-started) for the `cosign verify` commands.
- **Minimal runtime image**: both binaries ship on
  `gcr.io/distroless/static:nonroot` - no shell, no package manager, a
  non-root user by default.
- **Hardened pod/container security**: both the operator and the
  generated portal-server run with `runAsNonRoot`,
  `allowPrivilegeEscalation: false`, all Linux capabilities dropped,
  `seccompProfile: RuntimeDefault`, and a read-only root filesystem.
- **Verified `restricted` Pod Security Standard compliance** - not just
  inspected by reading YAML: both the operator's namespace and
  `test/smoke-test`'s demo namespace carry
  `pod-security.kubernetes.io/enforce: restricted` labels and are
  confirmed to admit panoptikum's own pods with zero violations
  (`kubectl label --dry-run=server` against a real cluster). The Helm
  chart has no `Namespace` template of its own, so it never silently
  relabels an existing namespace you point it at - label your own
  namespace the same way if you want this enforced for your install too.
- **The portal-server never talks to the Kubernetes API**
  (`AutomountServiceAccountToken: false`) - only the operator itself
  needs cluster API access.
- **No session state handled in plaintext**: session and OIDC-handshake
  state travels only as AES-256-GCM encrypted/signed cookies (see
  [OIDC Configuration](#oidc-configuration)), using a key you provide
  (`cookieSecretRef`), never generated or stored server-side.
- **RBAC aggregation, not broad default access**: the convenience
  per-CRD admin/editor/viewer `ClusterRole`s (opt-in,
  `rbac.helpers.enabled` in the chart) aggregate into Kubernetes' own
  `admin`/`edit`/`view` roles rather than requiring separate
  `RoleBinding`s - see the [chart README](charts/chart/README.md#rbac-aggregation).

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

## Full Example

```yaml
apiVersion: panoptikum.panoptikum.dev/v1alpha1
kind: UserAuthentication
metadata:
  name: keycloak
  namespace: management-portal
spec:
  type: OIDC
  cookieSecretRef:
    name: keycloak-client
    key: cookie-secret
  oidc:
    issuerURL: https://keycloak.example.com/realms/example
    clientID: management-portal
    clientSecretRef:
      name: keycloak-client
      key: client-secret
---
apiVersion: panoptikum.panoptikum.dev/v1alpha1
kind: Portal
metadata:
  name: main
  namespace: management-portal
spec:
  host: panoptikum.example.com
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
      clusterIssuer: letsencrypt-prod
---
apiVersion: panoptikum.panoptikum.dev/v1alpha1
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
apiVersion: panoptikum.panoptikum.dev/v1alpha1
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

## End-to-end example

[test/smoke-test/](test/smoke-test/) contains a full, manual walkthrough
of the whole stack (operator, portal-server, OIDC login, reverse proxy)
running in a local `kind` cluster. See its README for setup steps.
