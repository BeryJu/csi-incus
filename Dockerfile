FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /csi-incus-driver ./cmd/csi-incus-driver

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    e2fsprogs xfsprogs util-linux && rm -rf /var/lib/apt/lists/*
COPY --from=build /csi-incus-driver /csi-incus-driver
ENTRYPOINT ["/csi-incus-driver"]
