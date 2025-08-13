FROM docker.io/library/golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . /src
RUN go build ./cmd/orches

FROM registry.access.redhat.com/ubi9/ubi
RUN dnf install -y git-core && dnf clean all
COPY --from=builder /src/orches /usr/local/bin/orches
ENTRYPOINT ["/usr/local/bin/orches"]
WORKDIR /usr/local/bin
