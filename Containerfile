ARG GO_VERSION=1.25.3
FROM docker.io/golang:${GO_VERSION} AS builder

WORKDIR /app
COPY . .

RUN CGO_ENABLED=0 go build .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /app/oxide-cloud-controller-manager /usr/bin/oxide-cloud-controller-manager

ENTRYPOINT ["oxide-cloud-controller-manager"]
