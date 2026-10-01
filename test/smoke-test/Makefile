# Automates the manual walkthrough in README.md. Run from this directory:
#   make cluster       # 1. kind cluster + ingress-nginx
#   make image         # 2. build+load the operator image, install+deploy
#   make fix-coredns   # 3. patch in-cluster DNS for nanoidp's hostname
#   make apply         # 4. apply the example CRs/Deployments
# or just `make` to run all four in order. `make clean` tears the
# cluster back down.

CLUSTER_NAME ?= panoptikum-smoke-test
IMG ?= panoptikum:smoke-test
REPO_ROOT := $(abspath ../..)
NAMESPACE := panoptikum-demo
HOSTS := idp.127.0.0.1.nip.io portal.127.0.0.1.nip.io

.PHONY: all
all: cluster image fix-coredns apply

.PHONY: cluster
cluster: ## 1. Create the kind cluster and install ingress-nginx.
	kind create cluster --config kind-config.yaml
	kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml
	kubectl wait --namespace ingress-nginx \
		--for=condition=ready pod \
		--selector=app.kubernetes.io/component=controller \
		--timeout=120s

.PHONY: image
image: ## 2. Build the operator image and deploy it into the cluster.
	$(MAKE) -C $(REPO_ROOT) docker-build IMG=$(IMG)
	kind load docker-image $(IMG) --name $(CLUSTER_NAME)
	$(MAKE) -C $(REPO_ROOT) install deploy IMG=$(IMG)

.PHONY: fix-coredns
fix-coredns: ## 3. Patch CoreDNS so pods can reach nanoidp's external hostname.
	INGRESS_IP=$$(kubectl get svc -n ingress-nginx ingress-nginx-controller -o jsonpath='{.spec.clusterIP}'); \
	kubectl get configmap coredns -n kube-system -o jsonpath='{.data.Corefile}' > /tmp/panoptikum-smoke-test-Corefile.orig; \
	awk -v ip="$$INGRESS_IP" -v hosts="$(HOSTS)" ' \
		/^[[:space:]]*kubernetes / && !done { \
			print "    hosts {"; \
			print "        " ip " " hosts; \
			print "        fallthrough"; \
			print "    }"; \
			done = 1; \
		} \
		{ print } \
	' /tmp/panoptikum-smoke-test-Corefile.orig > /tmp/panoptikum-smoke-test-Corefile.patched; \
	kubectl patch configmap coredns -n kube-system --type=json \
		-p="[{\"op\": \"replace\", \"path\": \"/data/Corefile\", \"value\": $$(jq -Rs . < /tmp/panoptikum-smoke-test-Corefile.patched)}]"
	kubectl rollout restart deployment/coredns -n kube-system
	kubectl rollout status deployment/coredns -n kube-system --timeout=60s

.PHONY: undo-coredns
undo-coredns: ## Revert the CoreDNS patch from fix-coredns.
	kubectl patch configmap coredns -n kube-system --type=json \
		-p="[{\"op\": \"replace\", \"path\": \"/data/Corefile\", \"value\": $$(jq -Rs . < /tmp/panoptikum-smoke-test-Corefile.orig)}]"
	kubectl rollout restart deployment/coredns -n kube-system

.PHONY: apply
apply: ## 4. Apply the example CRs and wait for everything to come up.
	kubectl apply -k .
	kubectl wait --namespace $(NAMESPACE) \
		--for=condition=ready pod \
		--all \
		--timeout=180s

.PHONY: clean
clean: ## Delete the whole kind cluster.
	kind delete cluster --name $(CLUSTER_NAME)
