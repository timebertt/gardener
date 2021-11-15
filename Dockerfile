#############      builder       #############
FROM golang:1.17.2 AS builder

WORKDIR /go/src/github.com/gardener/gardener
COPY . .

ARG EFFECTIVE_VERSION

RUN make install EFFECTIVE_VERSION=$EFFECTIVE_VERSION

############# base
FROM alpine:3.13.6 AS base

#############      apiserver     #############
FROM base AS apiserver

RUN apk add --update tzdata

COPY --from=builder /go/bin/gardener-apiserver /gardener-apiserver

WORKDIR /

ENTRYPOINT ["/gardener-apiserver"]

############# controller-manager #############
FROM base AS controller-manager

RUN apk add --update tzdata

COPY --from=builder /go/bin/gardener-controller-manager /gardener-controller-manager
COPY charts /charts

WORKDIR /

ENTRYPOINT ["/gardener-controller-manager"]

############# scheduler #############
FROM base AS scheduler

COPY --from=builder /go/bin/gardener-scheduler /gardener-scheduler

WORKDIR /

ENTRYPOINT ["/gardener-scheduler"]

############# gardenlet #############
FROM base AS gardenlet

RUN apk add --update openvpn tzdata

COPY --from=builder /go/bin/gardenlet /gardenlet
COPY charts /charts

WORKDIR /

ENTRYPOINT ["/gardenlet"]

############# admission-controller #############
FROM base AS admission-controller

COPY --from=builder /go/bin/gardener-admission-controller /gardener-admission-controller

WORKDIR /

ENTRYPOINT ["/gardener-admission-controller"]

############# seed-admission-controller #############
FROM base AS seed-admission-controller

COPY --from=builder /go/bin/gardener-seed-admission-controller /gardener-seed-admission-controller

WORKDIR /

ENTRYPOINT ["/gardener-seed-admission-controller"]

############# resource-manager #############
FROM base AS resource-manager

COPY --from=builder /go/bin/gardener-resource-manager /gardener-resource-manager

WORKDIR /

ENTRYPOINT ["/gardener-resource-manager"]

############# landscaper-gardenlet #############
FROM base AS landscaper-gardenlet

COPY --from=builder /go/bin/landscaper-gardenlet /landscaper-gardenlet
COPY charts/gardener/gardenlet /charts/gardener/gardenlet
COPY charts/utils-templates /charts/utils-templates

WORKDIR /

ENTRYPOINT ["/landscaper-gardenlet"]

############# gardener-extension-provider-local #############
FROM golang:1.17.2 AS builder-provider-local

WORKDIR /go/src/github.com/gardener/gardener

# manually copy the packages we need to keep the build context slim
COPY charts charts
COPY cmd cmd
COPY extensions extensions
COPY pkg pkg
COPY third_party third_party
COPY vendor vendor

COPY go.mod go.sum ./

# `skaffold debug` sets SKAFFOLD_GO_GCFLAGS to disable compiler optimizations
ARG SKAFFOLD_GO_GCFLAGS
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -mod=vendor -gcflags="${SKAFFOLD_GO_GCFLAGS}" \
    -o /gardener-extension-provider-local ./cmd/gardener-extension-provider-local

FROM base AS gardener-extension-provider-local

# Define GOTRACEBACK to mark this container as using the Go language runtime
# for `skaffold debug` (https://skaffold.dev/docs/workflows/debug/).
ENV GOTRACEBACK=single

COPY charts/gardener/provider-local /charts/gardener/provider-local
COPY --from=builder-provider-local /gardener-extension-provider-local /gardener-extension-provider-local
ENTRYPOINT ["/gardener-extension-provider-local"]
