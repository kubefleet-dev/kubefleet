# Makefile for ApprovalRequest Controller

# Image settings
IMAGE_NAME ?= approval-request-controller
IMAGE_TAG ?= latest
REGISTRY ?=

# Build settings
GOARCH ?= amd64
GOOS ?= linux

# Tools
CONTROLLER_GEN_VERSION ?= v0.16.0
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Code Generation

.PHONY: manifests
manifests: ## Generate CRD manifests
	$(CONTROLLER_GEN) crd paths="./apis/..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: ## Generate DeepCopy code
	$(CONTROLLER_GEN) object:headerFile="../hack/boilerplate.go.txt" paths="./apis/..."

##@ Build

.PHONY: docker-build
docker-build: ## Build docker image
	docker buildx build \
		--file docker/Dockerfile \
		--output=type=docker \
		--platform=linux/$(GOARCH) \
		--build-arg GOARCH=$(GOARCH) \
		--tag $(IMAGE_NAME):$(IMAGE_TAG) \
		--build-context kubefleet=.. \
		..

.PHONY: docker-push
docker-push: ## Push docker image
	docker push $(REGISTRY)$(IMAGE_NAME):$(IMAGE_TAG)

##@ Development

.PHONY: run
run: ## Run controller locally
	cd .. && go run ./approval-request-controller/cmd/approvalrequestcontroller/main.go

##@ Deployment

.PHONY: install
install: ## Install helm chart
	helm install approval-request-controller ./charts/approval-request-controller \
		--namespace fleet-system \
		--create-namespace \
		--set image.repository=$(IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG)

.PHONY: upgrade
upgrade: ## Upgrade helm chart
	helm upgrade approval-request-controller ./charts/approval-request-controller \
		--namespace fleet-system \
		--set image.repository=$(IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG)

.PHONY: uninstall
uninstall: ## Uninstall helm chart
	helm uninstall approval-request-controller --namespace fleet-system

##@ Kind

.PHONY: kind-load
kind-load: docker-build ## Build and load image into kind cluster
	kind load docker-image $(IMAGE_NAME):$(IMAGE_TAG) --name hub
