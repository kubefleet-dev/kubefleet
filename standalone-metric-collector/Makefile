# Image URL to use for building/pushing image targets
REGISTRY ?= ghcr.io/kubefleet-dev
IMAGE_NAME ?= metric-collector
TAG ?= latest
IMG ?= $(REGISTRY)/$(IMAGE_NAME):$(TAG)

# Go parameters
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Directories
ROOT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
TOOLS_DIR := hack/tools
TOOLS_BIN_DIR := $(abspath $(TOOLS_DIR)/bin)

# Binaries
CONTROLLER_GEN_VER := v0.16.0
CONTROLLER_GEN_BIN := controller-gen
CONTROLLER_GEN := $(abspath $(TOOLS_BIN_DIR)/$(CONTROLLER_GEN_BIN)-$(CONTROLLER_GEN_VER))

GOIMPORTS_VER := latest
GOIMPORTS_BIN := goimports
GOIMPORTS := $(abspath $(TOOLS_BIN_DIR)/$(GOIMPORTS_BIN)-$(GOIMPORTS_VER))

# Scripts
GO_INSTALL := ../hack/go-install.sh

# CRD Options
CRD_OPTIONS ?= "crd"

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Tooling

$(CONTROLLER_GEN):
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) sigs.k8s.io/controller-tools/cmd/controller-gen $(CONTROLLER_GEN_BIN) $(CONTROLLER_GEN_VER)

$(GOIMPORTS):
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) golang.org/x/tools/cmd/goimports $(GOIMPORTS_BIN) $(GOIMPORTS_VER)

##@ Development

.PHONY: manifests
manifests: $(CONTROLLER_GEN) ## Generate CRD manifests.
	$(CONTROLLER_GEN) \
		$(CRD_OPTIONS) paths="./apis/..." output:crd:artifacts:config=config/crd/bases
	@echo "Copying CRDs to helm chart..."
	@cp config/crd/bases/*.yaml charts/metric-collector/templates/crds/

.PHONY: generate
generate: $(CONTROLLER_GEN) ## Generate code (DeepCopy, etc).
	$(CONTROLLER_GEN) \
		object:headerFile="../hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: imports
imports: $(GOIMPORTS) ## Organize imports.
	$(GOIMPORTS) -local github.com/kubefleet-dev/standalone-metric-collector -w .

.PHONY: test
test: fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

##@ Build

.PHONY: build
build: fmt vet ## Build metric-collector binary.
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o bin/metric-collector ./cmd/metriccollector

.PHONY: run
run: fmt vet ## Run controller from your host.
	go run ./cmd/metriccollector/main.go

.PHONY: docker-build
docker-build: ## Build docker image.
	docker build -t ${IMG} -f docker/Dockerfile .

.PHONY: docker-push
docker-push: ## Push docker image.
	docker push ${IMG}

.PHONY: docker-build-push
docker-build-push: docker-build docker-push ## Build and push docker image.

##@ Deployment

.PHONY: helm-lint
helm-lint: ## Lint helm chart.
	helm lint charts/metric-collector

.PHONY: helm-template
helm-template: ## Template helm chart.
	helm template metric-collector charts/metric-collector \
		--set memberCluster.name=cluster-1 \
		--set hubCluster.url=https://hub-cluster:6443 \
		--set prometheus.url=http://prometheus.test-ns:9090

.PHONY: helm-package
helm-package: helm-lint ## Package helm chart.
	helm package charts/metric-collector -d dist/

.PHONY: helm-install
helm-install: ## Install helm chart.
	helm upgrade --install metric-collector charts/metric-collector \
		--namespace fleet-system --create-namespace \
		--set memberCluster.name=$(CLUSTER_NAME) \
		--set hubCluster.url=$(HUB_URL) \
		--set prometheus.url=$(PROMETHEUS_URL) \
		--set image.repository=$(REGISTRY)/$(IMAGE_NAME) \
		--set image.tag=$(TAG)

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall helm chart.
	helm uninstall metric-collector --namespace fleet-system

##@ CRD

.PHONY: install-crds
install-crds: ## Install CRDs into the K8s cluster.
	kubectl apply -f config/crd/bases/

.PHONY: uninstall-crds
uninstall-crds: ## Uninstall CRDs from the K8s cluster.
	kubectl delete -f config/crd/bases/

##@ Cleanup

.PHONY: clean
clean: ## Clean build artifacts.
	rm -rf bin/ dist/ cover.out
