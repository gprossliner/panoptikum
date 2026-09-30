# panoptikum smoke test

A manual (not automated, not wired into any Makefile target or CI)
end-to-end walkthrough: a real `kind` cluster, a real Ingress, a real
OIDC login round trip against [nanoidp](https://github.com/cdelmonte-zg/nanoidp),
and a real session cookie reaching a minimal backend app through the
portal-server's reverse proxy. See `docs/ARCHITECTURE.md` "Smoke Testing"
for the design rationale (why each piece is built the way it is).

Everything here is disposable demo material for a local `kind` cluster -
including the committed secret values - never point this at anything
real.

## Prerequisites

- `kind`, `kubectl`, `kustomize` (or a `kubectl` recent enough to have it
  built in, `kubectl kustomize ...`).
- A real browser reachable from wherever this devcontainer/VS Code is
  running (see "Devcontainer networking" below if that's not obvious).

## 1. Create the cluster and install ingress-nginx

```bash
kind create cluster --config examples/smoke-test/kind-config.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml
kubectl wait --namespace ingress-nginx \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=120s
```

## 2. Build and deploy the operator into this cluster

Must run **inside** the cluster (not `make run-operator`, which runs
outside it) - the operator self-introspects its own running Pod to learn
its image (see `docs/ARCHITECTURE.md` Decision 10), which only works
running as a real Pod. From the repo root (not this directory):

```bash
export IMG=panoptikum:smoke-test
make docker-build IMG=$IMG
kind load docker-image $IMG --name panoptikum-smoke-test
make install deploy IMG=$IMG
```

## 3. Fix in-cluster DNS for nanoidp's external hostname

The portal-server pod calls nanoidp's `token_endpoint`/`jwks_uri`
server-to-server, using the *same* external hostname
(`idp.127.0.0.1.nip.io`) the browser uses for `/authorize` - OIDC
discovery only has one hostname, no per-endpoint override. `nip.io`
resolves to `127.0.0.1` for anyone who asks, but inside the portal-server
pod's own network namespace that's just the pod's own loopback, not the
ingress controller. Patch CoreDNS with a `hosts` entry pointing both
demo hostnames at `ingress-nginx-controller`'s ClusterIP:

```bash
INGRESS_IP=$(kubectl get svc -n ingress-nginx ingress-nginx-controller -o jsonpath='{.spec.clusterIP}')

kubectl get configmap coredns -n kube-system -o jsonpath='{.data.Corefile}' > /tmp/Corefile.orig

# Splice a "hosts" block in right before the existing "kubernetes" plugin
# line, rather than hardcoding a full Corefile - kind's default varies
# across versions, this only adds to whatever is actually there.
sed "/^ *kubernetes /i\\    hosts {\\n        $INGRESS_IP idp.127.0.0.1.nip.io portal.127.0.0.1.nip.io\\n        fallthrough\\n    }" \
  /tmp/Corefile.orig > /tmp/Corefile.patched

# A JSON Patch replacing only the Corefile key - unlike `kubectl create
# configmap --dry-run | apply`, this leaves every other key/label/
# annotation on the ConfigMap untouched. jq -Rs handles the multi-line
# string's JSON-quoting/escaping, not hand-written \n embedding.
kubectl patch configmap coredns -n kube-system --type=json \
  -p="[{\"op\": \"replace\", \"path\": \"/data/Corefile\", \"value\": $(jq -Rs . < /tmp/Corefile.patched)}]"

kubectl rollout restart deployment/coredns -n kube-system
```

To undo later:

```bash
kubectl patch configmap coredns -n kube-system --type=json \
  -p="[{\"op\": \"replace\", \"path\": \"/data/Corefile\", \"value\": $(jq -Rs . < /tmp/Corefile.orig)}]"
kubectl rollout restart deployment/coredns -n kube-system
```

or just delete the whole `kind` cluster.

(No equivalent fix is needed for the browser, or for tools run directly
in this devcontainer's own terminal - see "Devcontainer networking"
below for why.)

## 4. Apply the example

```bash
kubectl apply -k examples/smoke-test/
```

Wait for everything to come up:

```bash
kubectl get pods -n panoptikum-demo -w
```

## 5. Log in

Open `https://portal.127.0.0.1.nip.io/` in a browser (accept the
self-signed certificate warning - that's `ingress-nginx`'s own default
cert, expected, see `docs/ARCHITECTURE.md`). You'll be redirected to
nanoidp's persona picker (`alice`/`bob`, no password) over plain
`http://idp.127.0.0.1.nip.io/`, then back to the portal shell, logged in.

Click "Sample App" in the nav. The embedded iframe shows the sample
app's plain-text echo, including the `X-Forwarded-User` header the
portal-server injected - proof the whole chain (OIDC login → session
cookie → auth middleware → reverse proxy → trusted header injection)
worked end to end.

Visit `/_panoptikum/logout`, then log in again as the *other* persona to
see the header change.

## Devcontainer networking

If you're running this inside this repo's own `.devcontainer`, one more
network layer is worth understanding (see `docs/ARCHITECTURE.md` "Smoke
Testing" for the full comparison against a flat `docker-compose`-based
devcontainer, which needs a different fix entirely):

- **Browser ↔ devcontainer**, and **devcontainer-local tooling (`curl`,
  `go test`) ↔ the cluster**: both already work with no extra bridge.
  `.devcontainer/devcontainer.json` uses the `docker-in-docker` feature,
  so `kind`'s `extraPortMappings` (hostPort 80/443) bind directly onto
  the devcontainer's *own* loopback - the same as running
  `docker run -p 80:80` locally inside it. VS Code's normal port
  forwarding (or the `$BROWSER` tool) bridges the rest.
- **The portal-server pod ↔ the external hostname**: does *not* come for
  free - a third, separate network namespace beyond the devcontainer/dind
  boundary, with its own CoreDNS resolution. That's what step 3 above
  fixes.

## Cleanup

```bash
kind delete cluster --name panoptikum-smoke-test
```
