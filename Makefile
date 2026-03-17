VERSION ?= v0.4.0
GO_VERSION := $(shell go list -m -f '{{.GoVersion}}')
GO_CONTAINER_IMAGE ?= docker.io/golang:$(GO_VERSION)

# Set this to non-empty when building and pushing a release.
RELEASE ?=

CONTAINER_RUNTIME ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)
ifeq ($(CONTAINER_RUNTIME),)
$(error No container runtime found. Please install podman or docker)
endif

GIT_COMMIT_SHORT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Container image configuration.
IMAGE_REGISTRY ?= ghcr.io/oxidecomputer
IMAGE_NAME ?= oxide-cloud-controller-manager
IMAGE_TAG ?= $(patsubst v%,%,$(VERSION))$(if $(RELEASE),,-$(GIT_COMMIT_SHORT))
IMAGE_FULL ?= $(if $(IMAGE_REGISTRY),$(IMAGE_REGISTRY)/)$(IMAGE_NAME):$(IMAGE_TAG)

# Helm chart configuration.
HELM_CHART_DIR ?= charts/oxide-cloud-controller-manager
HELM_CHART_REGISTRY ?= oci://ghcr.io/oxidecomputer/helm-charts
HELM ?= go tool -modfile tools/go.mod helm

.PHONY: test
test:
	@echo "Running tests in container..."
	$(CONTAINER_RUNTIME) build \
		--file Containerfile \
		--build-arg GO_CONTAINER_IMAGE=$(GO_CONTAINER_IMAGE) \
		--build-arg VERSION=$(VERSION) \
		--target builder \
		--tag $(if $(IMAGE_REGISTRY),$(IMAGE_REGISTRY)/)$(IMAGE_NAME)-builder:$(IMAGE_TAG) \
		.
	$(CONTAINER_RUNTIME) run --rm $(IMAGE_NAME)-builder:$(IMAGE_TAG) go test -v ./...

.PHONY: build
build:
	@echo "Building container image: $(IMAGE_FULL)"
	$(CONTAINER_RUNTIME) build \
		--file Containerfile \
		--build-arg GO_CONTAINER_IMAGE=$(GO_CONTAINER_IMAGE) \
		--build-arg VERSION=$(VERSION) \
		--annotation org.opencontainers.image.description='Oxide Cloud Controller Manager' \
		--annotation org.opencontainers.image.source=https://github.com/oxidecomputer/oxide-cloud-controller-manager \
		--tag $(IMAGE_FULL) \
		.

.PHONY: push
push:
	@echo "Pushing container image: $(IMAGE_FULL)"
	$(CONTAINER_RUNTIME) push $(IMAGE_FULL)

.PHONY: helm-set-version
helm-set-version:
	@sed -i 's/^version: .*/version: "$(patsubst v%,%,$(VERSION))"/' $(HELM_CHART_DIR)/Chart.yaml
	@sed -i 's/^appVersion: .*/appVersion: "$(patsubst v%,%,$(VERSION))"/' $(HELM_CHART_DIR)/Chart.yaml

.PHONY: manifest
manifest: helm-set-version
	@echo "Generating Kubernetes manifest: manifests/oxide-cloud-controller-manager.yaml"
	$(HELM) template oxide-cloud-controller-manager $(HELM_CHART_DIR) \
		--namespace kube-system \
		> manifests/oxide-cloud-controller-manager.yaml

.PHONY: helm-package
helm-package: helm-set-version
	$(HELM) package $(HELM_CHART_DIR)

.PHONY: helm-push
helm-push: helm-package
	$(HELM) push $(IMAGE_NAME)-$(patsubst v%,%,$(VERSION)).tgz \
		$(HELM_CHART_REGISTRY)
