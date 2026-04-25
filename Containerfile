ARG GO_CONTAINER_IMAGE

# Stage 1: Build the binary.
# Note: build under the host platform and cross-compile, rather than the
# default of emulating the target architecture with QEMU.
FROM --platform=$BUILDPLATFORM ${GO_CONTAINER_IMAGE} AS builder

ARG VERSION
ARG TARGETOS
ARG TARGETARCH

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X k8s.io/component-base/version.gitVersion=${VERSION}" \
    .

# Stage 2: Minimal container image.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /app/oxide-cloud-controller-manager /usr/bin/oxide-cloud-controller-manager

ENTRYPOINT ["oxide-cloud-controller-manager"]
