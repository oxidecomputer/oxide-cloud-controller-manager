FROM docker.io/golang:1.25.0 AS builder

WORKDIR /app
COPY . .

RUN CGO_ENABLED=0 go build .

FROM docker.io/debian:bookworm

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/oxide-cloud-controller-manager /usr/bin/oxide-cloud-controller-manager

ENTRYPOINT ["oxide-cloud-controller-manager"]
