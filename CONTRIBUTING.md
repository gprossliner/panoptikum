# Contributing

## Development environment

The primary development environment for this repository is the **VS Code
dev container** defined in [.devcontainer/devcontainer.json](.devcontainer/devcontainer.json).
It provisions everything needed to build, test, and run the operator and
portal-server:

- Go toolchain (`golang:1.26` base image)
- Docker-in-Docker, for building/loading images and running `kind` clusters
- `kubebuilder` and `kind` CLIs, installed by [.devcontainer/post-install.sh](.devcontainer/post-install.sh)

Open the repository in VS Code and choose "Reopen in Container" (or use the
Dev Containers CLI) to get a ready-to-use environment. Other setups (bare
host, Codespaces, etc.) may work but are not the supported baseline — if
something only works in the dev container, that's expected; please mention
any host-specific workarounds you need in your PR description rather than
changing shared tooling to work around them.

## Common tasks

Run these from the repository root, inside the dev container:

```bash
make manifests generate   # regenerate CRDs/RBAC/DeepCopy after editing api/*_types.go
make lint-fix              # auto-fix code style
make test                  # unit tests (envtest: real API server + etcd)
make run-operator          # run the operator locally (uses current kubeconfig context)
make run-server             # run the portal-server locally (no kubeconfig needed)
```

E2E tests (`make test-e2e`) spin up a dedicated `kind` cluster
(`panoptikum-test-e2e`) via Docker-in-Docker and must not be pointed at a
real cluster — see [AGENTS.md](AGENTS.md) for details.

## Starting the operator in the dev container

With a `kind` cluster reachable from your current kubeconfig context:

```bash
make install       # regenerates manifests and applies the CRDs to the cluster
make run-operator  # regenerates manifests/deepcopy, then runs the operator locally
```

Both targets regenerate manifests themselves, so no separate `make
manifests generate` step is needed beforehand.

## Project conventions

See [AGENTS.md](AGENTS.md) for repository structure, code generation rules,
and kubebuilder scaffolding conventions, and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
for the design rationale behind the API and controllers.
