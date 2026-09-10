FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /csi-incus-driver ./cmd/csi-incus-driver

FROM alpine:3.24
RUN apk add --no-cache e2fsprogs xfsprogs util-linux
COPY --from=build /csi-incus-driver /csi-incus-driver
ENTRYPOINT ["/csi-incus-driver"]
