VERSION ?= v0.1.0
GO_CONTAINER_IMAGE ?= docker.io/golang:1.25.3

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
