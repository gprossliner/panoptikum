# Architecture

## Background

The predecessor of this project is a `management-portal` Terraform module
built as a first-iteration PoC for the same capability in a private,
unrelated infrastructure repo. It renders:

- an `nginx` Deployment serving a static `index.html` shell, gated by
  `auth_request` subrequests to a sidecar `oauth2-proxy` container (OIDC
  against Keycloak);
- a hand-written `apps` map inside `index.html` (label + path prefix per
  app), rendering each app in an `<iframe>` and reflecting the iframe's
  in-app route into the parent URL hash for deep links;
- `nginx.conf` `proxy_pass` rules routing `/headlamp/`, `/grafana/`, etc. to
  in-cluster `Service`s, with `auth_request_set` copying the validated
  identity from oauth2-proxy into a trusted header (`X-WEBAUTH-USER`) that
  downstream apps (Grafana's `auth.proxy`, Headlamp's
  `unsafeUseServiceAccountToken`) trust unconditionally.

That module works, but every change to the app list or auth wiring is a
Terraform apply. There's no dynamic registration, no per-app ownership
(an app team can't register itself without touching the platform's
Terraform), and the whole stack depends on nginx + oauth2-proxy as separate
components with their own config surface (`nginx.conf`, oauth2-proxy env
vars) that has to be kept in sync by hand.

Panoptikum reimplements the same capability as a Kubernetes operator: the
app list becomes a Kubernetes resource (`AppRegistration`) that app owners
can create in their own namespace, and the OIDC + reverse-proxy logic moves
into a single Go binary (`go-oidc` + `golang.org/x/oauth2` + `net/http/httputil`
reverse proxy), removing the nginx/oauth2-proxy dependency entirely.

## Goals

- Declarative, dynamically-reconciled app registration (no redeploy to add
  an app).
- Apps can be registered from their own namespace/repo, without touching the
  portal's own manifests.
- Preserve the existing security properties of the oauth2-proxy setup
  (OIDC session handling, trusted header injection) while dropping the
  nginx + oauth2-proxy runtime dependency.
- Support more than one `Portal` per cluster (multi-tenant platforms, per
  environment, etc.), while keeping the common case (one portal) simple.

## Non-goals (initial release)

- Proxying to arbitrary external URLs as an `AppRegistration` backend (only
  in-cluster `Service`s are supported initially — see Decision 1).
- Passing anything beyond `$user` to apps via `AppAuthentication` headers —
  no groups/roles/other claims passthrough. Same restriction as the
  predecessor Terraform module (its `X-WEBAUTH-USER` header carried only the
  username).
- Supporting apps that can only be served at their origin's root path.
  `AppRegistration.spec.routing.pathPrefix` requires the backend app to
  support being mounted under a subpath (as Grafana/Headlamp already do in
  the predecessor Terraform module via `serve_from_sub_path`/`config.baseURL`).

### Why these are non-goals for now

- **Groups/roles**: the `AppAuthentication` header template already has
  room for a `$groups` variable alongside `$user`, but emitting the claim
  isn't the hard part — every backend has a different authorization model
  to map it into. Grafana needs its own `role_attribute_path`-style org/role
  mapping; Headlamp isn't header-driven authorization at all today (it uses
  a single shared `unsafeUseServiceAccountToken`), so the correct mechanism
  there would be Kubernetes' own user impersonation
  (`Impersonate-User`/`Impersonate-Group`, honored by the apiserver itself),
  not a header convention; apps like Prometheus/Jaeger have no user concept
  to map onto at all. That's N bespoke integrations, not one feature — out
  of scope until a real need picks one backend to solve first. The
  available workaround already fits the model: run separate `Portal`s with
  different `AppRegistration` sets as the coarse-grained authorization
  boundary, which isn't a regression versus the predecessor Terraform
  module (already all-or-nothing per portal).
- **Root-only apps**: making a root-only app work behind a path prefix
  without its own base-path support would mean rewriting response bodies
  (`nginx sub_filter`-style content mangling) — fragile in ways that don't
  go away with more engineering effort: it breaks on compressed responses
  (decompress/rewrite/recompress), can't touch URLs a SPA constructs
  client-side at runtime, and can invalidate CSP/SRI hashes by mutating
  HTML/JS bytes after the fact. Grafana and Headlamp both already avoid
  needing this via a native base-path config knob, which is what
  `routing.pathPrefix` leans on. If a genuinely root-only app shows up later, the
  clean fix is subdomain-based routing (`grafana.portal.example.com`)
  instead of path-based, so the app keeps its own origin untouched — never
  body rewriting.

## API resources

### `Portal`

One instance of the user-visible portal shell: an authenticated entry point
with a nav bar of registered apps.

```
spec:
  host: string                       # externally-visible hostname; used for the
                                      # OIDC redirect URI even if ingress.enabled
                                      # is false and the user fronts it themselves
  displayName: string                # optional, shown in the shell header;
                                      # defaults to metadata.name
  customization:                     # optional, all fields optional
    logoURL: string                  # rendered as an <img src>, header logo
    faviconURL: string                # rendered as a <link href>
    backgroundColor: string          # header background, e.g. "#303030"
    accentColor: string              # active nav link / highlight color
  userAuthenticationRef:
    name: string
    namespace: string (optional, defaults to same namespace)
  allowedAppNamespaces: string        # regex (RE2), matched anchored against
                                      # an AppRegistration's namespace;
                                      # default ".+" (any namespace) — see
                                      # Decision 8
  ingress:
    enabled: bool                    # false: no Ingress created, user provides their own routing
    ingressClassName: string
    tls:
      clusterIssuer: string          # cert-manager ClusterIssuer name
status:
  conditions: [{type: Ready, ...}]
  appRegistrations:                  # back-refs, bound AppRegistrations
    - name: string
      namespace: string
```

### `UserAuthentication`

The OIDC configuration used to authenticate the human opening the portal.

```
spec:
  issuerURL: string
  clientID: string
  clientSecretRef:
    name: string
    key: string
  cookieSecretRef:                   # symmetric key, shared by every proxy
    name: string                     # replica, encrypts/signs session +
    key: string                      # handshake-state cookies (see Decision 7)
  scopes: [string]                   # default: [openid, profile, email]
  allowUnverifiedEmail: bool
status:
  conditions: [{type: Ready, ...}]
  portals:                           # back-refs, bound Portals
    - name: string
      namespace: string
```

### `AppAuthentication`

How the portal vouches for the logged-in user to a backend app. Modeled as
a typed union so more mechanisms can be added later without a new CRD.

```
spec:
  type: ProxyAuthentication          # enum, only variant for now
  proxyAuthentication:
    headers:                         # header name -> template
      X-Web-User: $user              # $user / $email / $groups
status:
  conditions: [{type: Ready, ...}]
  appRegistrations:                  # back-refs, bound AppRegistrations
    - name: string
      namespace: string
```

### `AppRegistration`

One app entry in a `Portal`'s nav.

```
spec:
  portalRef:
    name: string
    namespace: string (optional, defaults to same namespace)
  appAuthenticationRef:
    name: string
    namespace: string (optional, defaults to same namespace)
  displayName: string               # optional, defaults to metadata.name
  routing:
    pathPrefix: string               # e.g. /grafana/ — only variant for now
  sortOrder: int
  backend:
    service:                         # only supported backend kind for now
      name: string
      namespace: string (optional, defaults to same namespace)
      port: int
status:
  conditions:
    - type: ResolvedRefs             # portalRef / appAuthenticationRef / backend all found
    - type: Accepted                 # bound into the Portal's routing table;
                                      # False + reason NamespaceNotAllowed if
                                      # this namespace doesn't match the
                                      # Portal's allowedAppNamespaces (Decision 8)
```

## Cross-cutting design decisions

### Decision 1: Backend targeting and app routing — typed unions, single variant for now

Both `AppRegistration.spec.backend` and `AppRegistration.spec.routing` are
typed unions with a single supported variant each (`service` and
`pathPrefix` respectively) for now. Modeled as unions rather than flat
fields (`serviceName`, `pathPrefix` at the top level) so a future variant
can be added as a pure addition, without breaking existing specs or
introducing a second, differently-shaped field for the same purpose:

- `backend`: a future `url:` variant for an app that isn't in-cluster.
- `routing`: a future `host:` variant for subdomain-based routing
  (`grafana.portal.example.com`), the identified alternative to body
  rewriting for apps that can't be served under a path prefix (see
  "Why these are non-goals for now" above).

### Decision 2: Multiple `Portal`s, cross-namespace references allowed

A cluster will typically have one `Portal`, but the API supports many
(different teams/environments). All cross-kind references
(`AppRegistration` -> `Portal`, `AppRegistration` -> `AppAuthentication`,
`Portal` -> `UserAuthentication`) accept an optional `namespace`, defaulting
to the referencing object's own namespace when omitted.

Cross-namespace references have no per-target-resource authorization
handshake (no Gateway-API-style `ReferenceGrant` equivalent) — a `Portal`
controls who may attach to it via `Portal.spec.allowedAppNamespaces`
instead (Decision 8), which is enough for the namespace-boundary-as-trust-
boundary case this is meant to cover.

### Decision 3: Secrets are pre-provisioned, never inline

`UserAuthentication.spec.clientSecretRef` (and any future secret-bearing
field) references an existing `Secret` by name/key; no CRD ever carries a
secret value inline. A Helm chart may optionally bootstrap a "default"
`Portal` + accompanying `Secret` for first-run convenience — exact semantics
(what it creates, whether it auto-generates values) are deferred to when the
chart is built.

### Decision 4: Control-plane vs. data-plane — separate processes, one image, config handoff via a generated `Secret`

The operator and the portal-server (data plane) are two separate processes
— two Go binaries (`cmd/operator`, `cmd/server`), each its own `main`
package, built via a multi-stage `Dockerfile` into a single container image
(exactly one image/tag to build, scan, and version) — selected via the
container's `command:`, rather than one process wearing both hats. Two
binaries rather than one with subcommands, specifically so the
portal-server executable never statically links `client-go`/
`controller-runtime` at all — its dependency graph (and what an SBOM/
vulnerability scan reports against it) matches its actual, much smaller
attack surface, reinforcing the "no Kubernetes API access" property below
at the compiled-artifact level, not just at runtime.

- Only the operator process has a Kubernetes API client / `ServiceAccount`
  with RBAC. It reconciles all four CRDs (including cross-namespace
  references, per Decision 2), merges everything a given `Portal` needs into
  a single JSON document, and writes it to a generated `Secret` (one per
  `Portal`, owned via `ownerReferences` so it's garbage-collected with it).
  It also owns creating that `Portal`'s `Deployment`/`Service`.
- The portal-server process never talks to the Kubernetes API — no
  `ServiceAccount` token mounted at all (`automountServiceAccountToken:
  false`). Its entire externally-reachable surface (OIDC callbacks, reverse
  proxying, session cookies) is isolated from anything that could touch
  cluster state; it only reads the mounted config `Secret` at startup.
- Rollout: the operator computes a deterministic hash of the merged config
  (built from typed Go structs, not string concatenation, so `json.Marshal`
  is stable), writes the `Secret`, and — only if the hash differs from the
  one currently stamped on the portal-server Deployment's pod-template
  annotation — patches that single annotation. The `Secret` write always
  happens before the annotation patch, never the reverse, so a rollout can
  never start against a stale `Secret`. The Deployment controller then
  performs a normal rolling restart, which mounts the updated `Secret` into
  fresh pods — the same "checksum/config" pattern Helm charts already use,
  no bespoke rollout logic needed. One hash annotation is enough (on
  `spec.template.metadata.annotations`); no separate Deployment-level copy.

Consequences: config-affecting changes to any bound `UserAuthentication`/
`AppAuthentication`/`AppRegistration`, even from a different namespace, must
be watched and mapped back to the `Portal`(s) they affect — the same watch
machinery the back-ref status lists (Decision 5) already require.

### Decision 5: Status conventions — `Accepted` + back-ref lists

Modeled after the Gateway API Route/Gateway status pattern:

- The **referencing** resource (`AppRegistration`, and `Portal` w.r.t. its
  `UserAuthentication`) carries conditions describing whether its references
  resolved (`ResolvedRefs`) and whether the target resource accepted it
  (`Accepted`).
- The **referenced** resource (`Portal`, `UserAuthentication`,
  `AppAuthentication`) carries a status list of the objects currently bound
  to it (e.g. `Portal.status.appRegistrations[]`), so `kubectl get portal -o
  yaml` shows what's actually wired up without having to search every
  namespace for matching `AppRegistration`s.

All CRDs use the standard `.status.conditions []metav1.Condition`
subresource shape.

### Decision 6: API versioning — start at `v1alpha1`

All CRDs (`apiextensions.k8s.io` `spec.versions`) start at `v1alpha1`, with a
single version ever served/stored. No conversion webhook is needed — that
machinery only applies when two or more versions with differing schemas are
served concurrently, which won't happen while there's only one version.
Schema evolution while still on `v1alpha1` is expected to be additive only
(new optional fields, defaults, widened enums); anything that would break
already-stored objects (renames, removals, new required fields, narrowed
enums) needs a real migration regardless of version name.

`v1alpha1` signals no compatibility guarantees, matching where the project
actually is this early in its lifecycle — no real-world usage yet to
confirm the CRD shapes against. The plan is to promote to `v1` at the first
major release, once that's happened.

### Decision 7: Session state — shared-secret encrypted cookies, no Redis

Both the post-login session and the transient OIDC handshake state
(`state`/`nonce`/PKCE `code_verifier`) are carried entirely client-side in
encrypted, signed cookies, using one symmetric key
(`UserAuthentication.spec.cookieSecretRef`) shared by every proxy replica.
Any replica can validate a cookie issued by any other, so a plain
round-robin `Service` works — no sticky sessions, no shared session store.
This matches oauth2-proxy's own default (`--session-store-type=cookie`),
which is exactly what the predecessor Terraform module already relies on
via its `cookie-secret`; Redis is an opt-in oauth2-proxy backend for large
sessions/centralized invalidation, never a requirement for multi-replica
correctness.

Consequences:

- Multiple replicas are supported from the first version — no reason to
  restrict v1alpha1 to a single replica.
- Session payload must stay within browser cookie size limits (~4KB per
  cookie) — store the minimal identity claims needed for
  `AppAuthentication` header templating rather than full ID/refresh tokens;
  revisit cookie-chunking (as oauth2-proxy does) only if this proves
  insufficient.
- Trade-off: no server-side session revocation. Forcing out a single
  session early isn't possible without shared state; the blunt "log out
  everyone" lever is rotating the shared secret. Acceptable for an internal
  admin portal; revisit if a real per-session revocation requirement shows
  up.

### Decision 8: Cross-namespace `AppRegistration` authorization — `Portal.spec.allowedAppNamespaces` regex

`Portal.spec.allowedAppNamespaces` is a regex string (default `".+"`, i.e.
any namespace) that an `AppRegistration`'s namespace must match to be bound.
A non-matching `AppRegistration` gets `status.conditions[Accepted]` set to
`False` with reason `NamespaceNotAllowed`, rather than being rejected
outright — it stays visible via `kubectl get`, just not wired up.

Chosen over an explicit namespace allow-list array because multi-tenant
clusters are commonly namespace-prefix-organized (`team-a-.+`), and
alternatives can be OR-ed (`grafana|jaeger`) in one field, with a trivial
default (`.+`) that preserves today's wide-open behavior.

Implementation notes:

- The operator must anchor the pattern itself (`^(?:pattern)$`) before
  matching — Go's `regexp` package matches unanchored by default, so an
  unanchored `team-a-.+` would also match `evil-team-a-foo`, silently
  defeating the allow-list.
- Go's `regexp` package uses RE2, which guarantees linear-time matching —
  no catastrophic-backtracking/ReDoS risk from an admin-supplied pattern.
- An invalid pattern (fails to compile) is surfaced on the `Portal` itself
  (its `Ready` condition), not silently treated as "allow nothing"/"allow
  everything".
- No new watch machinery needed: the `AppRegistration` reconciler already
  fetches its bound `Portal` to attach itself, so a change to
  `allowedAppNamespaces` re-evaluates existing bindings for free.
- Scope: this only gates `AppRegistration` -> `Portal` binding. The
  `AppRegistration` -> `AppAuthentication` reference has no analogous
  allow-list yet — left as a follow-up if that turns out to matter in
  practice.

### Decision 9: Project tooling — kubebuilder, not operator-sdk

Scaffolding and codegen use kubebuilder (`api/<version>/*_types.go` with
kubebuilder markers, `controller-gen` for CRD YAML/deepcopy/RBAC
`ClusterRole` generation, `envtest` for reconciler tests) rather than
operator-sdk. operator-sdk's Go support is itself built on the same
kubebuilder plugin machinery — its only real addition is OLM
bundle/CSV packaging for OperatorHub distribution, which isn't a goal here
(install path is a Helm chart, per earlier discussion) and can be layered
in later without redoing anything if that ever changes. Plain kubebuilder
also imposes less opinionated project structure, which matters here since
the repo needs two separate `main` packages (`cmd/operator`, `cmd/server`,
per Decision 4) rather than the single-binary layout most scaffolding
assumes.

### Decision 10: Portal-server Deployment image — self-introspection; resources/replicas are real Portal fields

Creating the portal-server `Deployment` (Decision 4) needs an image/tag,
`resources`, and a replica count:

- **Image**: Decision 4 already establishes "exactly one image, selected
  via the container's `command:`" as a hard invariant, not a per-`Portal`
  choice — so it's derived, never configured. At startup the operator
  reads its own namespace from the standard mounted service-account file
  (`/var/run/secrets/kubernetes.io/serviceaccount/namespace`, present on
  any pod with a token mounted) and its own pod name from `os.Hostname()`
  (a Deployment-managed pod's hostname defaults to its own name), does a
  `Get` on its own `Pod`, and reads the `manager` container's `.image`
  from the live object. This needs one new RBAC grant (`get` on `pods`)
  but zero manifest/Helm changes — the same `image:` field already
  required to run the operator is automatically correct for the
  portal-server too, since it's read live rather than duplicated into a
  second field that could drift out of sync.
- **`resources` and replica count**: unlike the image, these genuinely are
  per-`Portal` choices, so they're real fields:
  `Portal.spec.server.{replicas,resources}` (`PortalServerConfig`).
  `resources` reuses the standard `corev1.ResourceRequirements` type
  rather than a bespoke shape. Both default via CRD defaulting when
  omitted (`replicas: 1`, conservative `resources`) — the `server` field
  itself defaults to `{}` so the nested per-field defaults apply even when
  a `Portal` doesn't mention `server` at all (Kubernetes structural-schema
  defaulting recurses into a defaulted-in object). `replicas` defaults to
  1, not 2 — despite Decision 7 establishing multi-replica *support* from
  day one, defaulting to a single replica is the less surprising choice;
  users who want more set `spec.server.replicas` explicitly.


## Reconciliation design

Four reconcilers, one per CRD (`Portal`, `UserAuthentication`,
`AppAuthentication`, `AppRegistration`). The cross-cutting rule that keeps
them from stepping on each other:

**Single writer per status: a reconciler only ever writes its own kind's
`status`, never another kind's.** Concretely:

- `AppRegistrationReconciler` fetches (read-only) its referenced `Portal`,
  `AppAuthentication`, and backend `Service`, and sets **its own**
  `ResolvedRefs`/`Accepted` conditions accordingly (`Accepted` requires
  evaluating the target `Portal`'s `allowedAppNamespaces` regex, per
  Decision 8 — read the Portal, don't write it).
- `PortalReconciler` computes its own `status.appRegistrations[]` back-ref
  list by **listing** `AppRegistration`s bound to it (`Accepted=True`) —
  never by having `AppRegistrationReconciler` patch `Portal.status`
  directly. Same pattern for `UserAuthentication.status.portals[]`
  (written by `UserAuthenticationReconciler`, listing `Portal`s that
  reference it) and `AppAuthentication.status.appRegistrations[]`.

This avoids two different controllers racing to update the same object's
status (which would otherwise need optimistic-concurrency retries), and
matches Decision 5: the referenced resource always owns its own back-ref
status.

Listing "who references me" efficiently (and cheaply, inside a watch
mapping function — see below) requires a cached field indexer per
cross-reference field (`mgr.GetFieldIndexer().IndexField`), e.g. indexing
`AppRegistration` on `spec.portalRef` and `spec.appAuthenticationRef`, and
`Portal` on `spec.userAuthenticationRef`.

### Propagating changes across kinds

controller-runtime enqueues a reconcile for a kind's own `For(...)` watch
on any `Create`/`Update`/`Delete` of that kind — including status-subresource-
only updates — which would otherwise make a reconciler re-trigger itself
every time it writes its own status. Each `For(...)` watch uses
`predicate.GenerationChangedPredicate{}` to suppress that: a status-only
update never changes `.metadata.generation`, so a reconciler's own status
write does not requeue itself. (Caveat: setting `deletionTimestamp` also
doesn't bump `generation` — irrelevant here since none of these four kinds
use finalizers, per the "Consequences" note in Decision 4: cleanup of the
generated `Secret`/`Deployment` is via `ownerReferences` GC, not a
finalizer. If a finalizer is ever added to one of these kinds, its watch
predicate must explicitly let deletion-timestamp changes through.)

Reacting to a *different* kind changing (e.g. `Portal` needing to
recompute its merged config when a bound `AppRegistration`'s `Accepted`
condition flips) requires an explicit `Watches(...)` with a mapping
function — this is never automatic across kinds, and here the predicate
must *not* filter out status-only changes, since the very thing being
watched for is a status condition flip:

- `PortalReconciler`: `For(&Portal{})` (generation-changed only) +
  `Watches(&AppRegistration{})` (map → owning `Portal`, via the
  `spec.portalRef` indexer) + `Watches(&UserAuthentication{})` (map →
  `Portal`s referencing it) + `Watches(&AppAuthentication{})` (map
  transitively, via the `AppRegistration`s that use it, to their `Portal`)
  + `Owns(&corev1.Secret{})`/`Owns(&appsv1.Deployment{})` for the
  generated config `Secret` and portal-server `Deployment`.
- `AppRegistrationReconciler`: `For(&AppRegistration{})` +
  `Watches(&Portal{})` (so an `allowedAppNamespaces` edit re-evaluates
  `Accepted` on every bound `AppRegistration`) + `Watches(&AppAuthentication{})`
  (for `ResolvedRefs`) + `Watches(&corev1.Service{})` (backend existence,
  for `ResolvedRefs`).
- `UserAuthenticationReconciler`: `For(&UserAuthentication{})` +
  `Watches(&corev1.Secret{})` (its `clientSecretRef`/`cookieSecretRef`).
- `AppAuthenticationReconciler`: `For(&AppAuthentication{})` only — leaf
  kind, no outgoing references.

### `ObservedGeneration`

Every condition written by any of the four reconcilers sets
`ObservedGeneration` to the object's `.metadata.generation` at reconcile
time. `apimeta.SetStatusCondition` (`k8s.io/apimachinery/pkg/api/meta`)
does not derive this automatically — it must be set explicitly on the
`metav1.Condition` passed in. This lets a user (or `kubectl`) tell whether
a displayed condition reflects the latest spec: if
`status.conditions[x].observedGeneration < metadata.generation`, the
reconciler hasn't caught up yet (queue backlog, or stuck retrying an
earlier generation).

### Suggested build order

1. `UserAuthentication` + `AppAuthentication` — leaf kinds, no
   cross-references; get the basic `Ready`-condition reconciler shape
   right first.
2. `AppRegistration` — introduces refs, field indexers, and the
   `ResolvedRefs`/`Accepted` pattern.
3. `Portal` — ties everything together: cross-kind watches, the
   `status.appRegistrations[]` back-ref list, and (a later iteration) the
   merged-config `Secret` + `Deployment` from Decision 4.

## Testing strategy

Four tiers, deliberately scoped so each one tests only what the tiers
below it structurally cannot — no tier re-covers what a cheaper tier
already proves:

1. **Pure unit tests** (`go test`, no Kubernetes involved). For logic
   extracted into plain functions with no API-server dependency: the
   `allowedAppNamespaces` regex anchoring/matching (Decision 8), header
   template rendering (`AppAuthentication`), the config-merge/hash
   computation (Decision 4). Fastest and most exhaustive tier
   (table-driven); business logic should be pulled out of `Reconcile()`
   into standalone functions specifically so it lands here.
2. **`envtest` + direct `Reconcile()` calls** — a real `kube-apiserver` +
   `etcd` (no kubelet, no built-in controllers), calling
   `reconciler.Reconcile(ctx, req)` directly rather than running the
   manager. Deterministic, no watch/timing flakiness. This is where the
   bulk of controller tests live: exhaustive coverage of one reconciler's
   own logic (ref-not-found, regex rejection, condition transitions,
   `ResolvedRefs`/`Accepted` combinations).
3. **`envtest` + the real manager running** (`mgr.Start(ctx)`, assertions
   via `Eventually()` against objects created through the real client —
   never calling `Reconcile` directly). Scope is deliberately narrow: one
   test per edge in the cross-kind watch graph (see "Propagating changes
   across kinds" above), proving only that a **status-only** mutation on
   the watched kind reaches the dependent reconciler — e.g. that updating
   `AppRegistration.status` (and nothing else) causes `PortalReconciler`
   to run. That's the one property tier 2 cannot prove: a missing
   `Watches(...)`, a wrong field-indexer key, or an overly-broad predicate
   would break propagation silently without tier 2 ever noticing. Bounded
   by the number of watch edges, not by business-logic scenarios — the
   exhaustive condition/reason matrix stays in tier 2.
4. **True e2e** ([test/e2e/e2e_test.go](../test/e2e/e2e_test.go)): real
   image, dedicated `kind` cluster, deployed via kustomize, driven via
   `kubectl`. Catches what `envtest` structurally can't (RBAC gaps, image
   build issues, real container startup/probes, webhook TLS via
   cert-manager). Deferred until there's an actually deployable slice of
   the system to smoke-test — not useful yet while the CRDs/controllers
   are still placeholders.

## Error handling in reconcilers

Reconciler bodies are long chains of sequential, dependent lookups
(`Portal` → `UserAuthentication` → `Secret`; `AppRegistration` → `Portal` +
`AppAuthentication` + backend `Service`), which is exactly the shape where
repeating `if err != nil { return ctrl.Result{}, err }` after every client
call adds the most noise for the least value. Reconcilers use
[`github.com/gprossliner/xhdl`](https://github.com/gprossliner/xhdl) —
panic/recover-based structured error handling — to avoid that boilerplate:

```go
func (r *PortalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	err = xhdl.RunContext(ctx, func(xc xhdl.Context) {
		result = r.reconcile(xc, req)
	})
	return
}
```

with the actual logic taking `xhdl.Context` and calling `xc.Throw(err)`
after client calls instead of an explicit `if err != nil` check.
`xhdl.Context` embeds `context.Context` and `RunContext` wraps the
incoming one rather than starting fresh, so values/deadline/cancellation
(e.g. `logf.FromContext(ctx)`) still work unchanged after wrapping.

Rules for using it consistently:

- The `xhdl.RunContext` wrap must be at the very top of every
  `Reconcile` — a `Throw` deeper in the call stack with no enclosing
  `RunContext` panics uncaught (controller-runtime's own crash recovery
  keeps this from taking down the whole manager, but that Reconcile
  invocation won't return a clean `ctrl.Result`/`error`).
- `Throw` does not replace judgment calls that aren't really failures —
  e.g. `apierrors.IsNotFound` on a `Get` is still handled explicitly
  (usually "return, nothing to do") before falling through to
  `xc.Throw(err)` for genuine failures.
- Invisible to the testing strategy above: `Reconcile()` still returns the
  normal `(ctrl.Result, error)` signature, so tiers 2/3 need no changes.

## Server (portal-server)

`cmd/server` is the data-plane binary (Decision 4): one process, no
Kubernetes API access, handling both OIDC login and reverse-proxying to
registered apps. It never talks to the operator directly — its entire
input is the mounted `internal/portalconfig.Config` JSON file.

### Process shape

- Flags, not env vars (consistent with the rest of this binary's existing
  `--address`): `--config` (path to the mounted config JSON, default
  `/etc/panoptikum/config.json`), `--address` (listener bind address),
  `--log-level` (`debug`/`info`/`warn`/`error`).
- **Two separate log streams, mirroring the predecessor Terraform
  module's nginx `access_log`/`error_log` split**: access logs (one line
  per request, Apache Common Log Format, deliberately not configurable)
  go to **stdout**; status/application logs (structured, `log/slog` JSON)
  go to **stderr**. Lets log collection filter/route the two independently
  without parsing a mixed stream.

### Session & OIDC

- Session state is a small, encrypted/signed cookie (Decision 7) — no
  server-side store, any replica can validate any other's cookie. Only
  the claims actually needed for header templating are stored (just
  `$user` today, per the non-goals) — never full ID/refresh tokens.
- `/login`: redirects to the IdP (authorization code + PKCE, `state`/
  `nonce` in a short-lived handshake cookie), remembering the originally-
  requested URL so `/callback` can redirect back to it afterwards — the
  same role oauth2-proxy's `sign_in?rd=<url>` played in the predecessor.
- `/callback`: exchanges the code, validates the ID token
  (`coreos/go-oidc` + `golang.org/x/oauth2`, not hand-rolled — see
  Security considerations), sets the session cookie, redirects back.
- `/logout`: clears the session cookie.
- An auth middleware gates every proxied route: valid session → attach
  the user to the request context; missing/invalid → redirect to
  `/login?rd=<original-url>`. This collapses the predecessor's nginx
  `auth_request` + `error_page 401` subrequest pattern (a separate
  oauth2-proxy sidecar reached via an internal HTTP call per request)
  into a single in-process check — no subrequest machinery needed once
  OIDC and proxying live in the same binary.

### Reverse proxy

Requirements confirmed against the predecessor Terraform module's
`nginx.conf` (one hand-written `location` block per app, which
`AppRegistration` generalizes into data):

- **No path rewriting**: the full, unmodified request URI is forwarded to
  the backend as-is. Backend apps configure their own base path (Grafana
  `server.serve_from_sub_path`, Headlamp/Jaeger/Prometheus
  `baseURL`/`routePrefix`/equivalent) and expect to see it in the
  incoming request — matches `AppRegistration.spec.routing.pathPrefix`'s
  non-goal of supporting root-only apps. In practice this means never
  touching `r.URL.Path` before proxying.
- **Bare-prefix redirect**: a request for `/grafana` (no trailing slash)
  must `301` to `/grafana/` using the *trusted external* scheme/host, not
  whatever the reverse proxy sees locally — behind a TLS-terminating
  ingress, the portal-server only sees plain HTTP, so this must come from
  `X-Forwarded-Proto`/`X-Forwarded-Host`, trusted only from the actual
  ingress hop (see Security considerations).
- **WebSocket passthrough** is required (Headlamp's live pod logs/exec,
  Grafana Live). `net/http/httputil.ReverseProxy` handles `Upgrade`
  transparently in modern Go — confirm with an explicit test rather than
  assuming.
- **Trusted header injection** per `AppRegistration`'s resolved
  `AppAuthentication`: strip any client-supplied header with the same
  name before setting the authenticated value (see Security
  considerations — this is the exact bug class that would reopen the
  auth-bypass hole the whole header-injection design exists to close).

### Portal shell UI

A single static HTML/JS page (server-rendered with `config.portal`/
`config.apps` data, no separate frontend build): a nav bar generated from
`config.apps[]` (ordered by `sortOrder`), each app embedded in a
same-origin `<iframe>` (safe only because everything is path-based
routing behind one host), reflecting the embedded app's in-app route into
the parent page's URL hash for deep links/bookmarks — direct continuation
of the predecessor's same approach, just generated from `AppRegistration`
data instead of a hand-maintained `apps` map in `index.html`. The
predecessor fetched the logged-in user from oauth2-proxy's
`/oauth2/userinfo` endpoint client-side; the portal-server already knows
the user from its own session, so this becomes a server-rendered value
instead of a separate client-side fetch.

### Build order

1. Session cookie codec (encrypt/sign, pure/unit-testable, no HTTP).
2. OIDC login flow (`/login` + `/callback`).
3. Auth middleware.
4. Reverse proxy per app (exact-URI passthrough, trailing-slash redirect,
   header injection, WebSocket passthrough).
5. `/logout`.
6. Portal shell UI.
7. Wire into `main.go`; manual smoke test against a real cluster.

## Security considerations

- Secrets only via `Secret` references (Decision 3) — never inline in CRD
  specs.
- Only the operator process holds Kubernetes API credentials (Decision 4);
  the portal-server Deployment runs with no `ServiceAccount` token mounted
  at all, keeping any compromise of its internet-facing OIDC/reverse-proxy
  surface from reaching cluster state.
- The proxy must strip any client-supplied header matching a configured
  `ProxyAuthentication` header name before setting the trusted value —
  nginx did this implicitly via `auth_request_set` always overwriting; a
  naive Go reverse proxy that only *adds* a header without first deleting
  the inbound one re-opens the auth-bypass hole this design exists to close.
- OIDC flow (state/nonce/PKCE, session cookie encryption, `Secure`/
  `HttpOnly`/`SameSite=Lax`) should use `coreos/go-oidc` +
  `golang.org/x/oauth2` rather than a hand-rolled implementation, matching
  what oauth2-proxy provided.
- `X-Forwarded-Proto`/`X-Forwarded-Host` must only be trusted from the
  actual ingress hop, not from arbitrary clients, if the portal's `Service`
  is ever reachable other than through the cluster's Ingress.
- Cross-namespace references (Decision 2) mean a namespace can currently
  attach an `AppRegistration` to any `Portal` it can name unless restricted
  via `Portal.spec.allowedAppNamespaces` (Decision 8) — wide open by default,
  matching Decision 2.
- `Portal.spec.customization` is intentionally limited to URLs and color
  strings, rendered via `<img src>`/`<link href>`/CSS custom properties —
  no freeform HTML/CSS/JS field, to keep it from becoming a stored-XSS
  vector for whoever can create/edit a `Portal`.

## Decided naming

- API group: `panoptikum.dev` (the domain itself is not currently
  registered — fine for a Kubernetes API group, which doesn't need to
  resolve, but a reminder not to rely on it for anything that does, e.g.
  webhook TLS/cert issuance or email).
