VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo v0.0.0-dev)

CONTAINER_RUNTIME ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

# Container image configuration.
IMAGE_REGISTRY ?= ghcr.io/oxidecomputer
IMAGE_NAME ?= oxide-cloud-controller-manager
IMAGE_TAG ?= $(patsubst v%,%,$(VERSION))
IMAGE_FULL ?= $(if $(IMAGE_REGISTRY),$(IMAGE_REGISTRY)/)$(IMAGE_NAME):$(IMAGE_TAG)
export KO_DEFAULTBASEIMAGE ?= gcr.io/distroless/static-debian13:nonroot

# Helm chart configuration.
HELM_CHART_DIR ?= charts/oxide-cloud-controller-manager
HELM_CHART_REGISTRY ?= oci://ghcr.io/oxidecomputer/helm-charts
HELM ?= go tool -modfile tools/go.mod helm
ARTIFACTS ?= _artifacts
GORELEASER ?= go tool -modfile tools/go.mod goreleaser
KO ?= go tool -modfile tools/go.mod ko

.PHONY: test
test:
	go test -v ./...

# Builds a host-arch image with ko, then loads it into podman or docker.
.PHONY: build
build:
ifeq ($(CONTAINER_RUNTIME),)
	$(error No container runtime found. Please install podman or docker)
endif
	@echo "Building container image: $(IMAGE_FULL)"
	mkdir -p $(ARTIFACTS)
	KO_DOCKER_REPO=$(if $(IMAGE_REGISTRY),$(IMAGE_REGISTRY)/)$(IMAGE_NAME) \
	GOFLAGS=-ldflags=-X=k8s.io/component-base/version.gitVersion=$(VERSION) \
		$(KO) build . \
		--bare \
		--tags $(IMAGE_TAG) \
		--platform linux/$(shell go env GOARCH) \
		--push=false \
		--tarball $(ARTIFACTS)/image.tar
	$(CONTAINER_RUNTIME) load --input $(ARTIFACTS)/image.tar

.PHONY: manifests
manifests:
	rm -rf $(ARTIFACTS)
	mkdir -p $(ARTIFACTS)/helm
	$(HELM) package $(HELM_CHART_DIR) \
		--version $(patsubst v%,%,$(VERSION)) \
		--app-version $(patsubst v%,%,$(VERSION)) \
		--destination $(ARTIFACTS)/helm
	$(HELM) template oxide-cloud-controller-manager $(ARTIFACTS)/helm/*.tgz \
		--namespace kube-system \
		> $(ARTIFACTS)/oxide-cloud-controller-manager.yaml

.PHONY: release-dry-run
release-dry-run:
	$(GORELEASER) release --snapshot --clean

.PHONY: release
release:
	$(GORELEASER) release --clean
	$(HELM) push $(ARTIFACTS)/helm/*.tgz $(HELM_CHART_REGISTRY)

.PHONY: fmt
fmt:
	@ echo "+ Formatting Go code..."
	@ go tool -modfile tools/go.mod golangci-lint fmt
	@ go tool -modfile tools/go.mod golangci-lint run --enable-only=goheader --fix

.PHONY: lint
lint:
	@ echo "+ Running Go linters..."
	@ go tool -modfile tools/go.mod golangci-lint run

.PHONY: dev
dev: fmt lint test
