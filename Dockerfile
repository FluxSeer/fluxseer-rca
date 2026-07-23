FROM --platform=$BUILDPLATFORM golang:1.26.2@sha256:b54cbf583d390341599d7bcbc062425c081105cc5ef6d170ced98ef9d047c716 AS builder

ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG GIT_DIRTY=unknown
ARG BUILD_DATE=unknown
ARG SOURCE_DATE_EPOCH=0
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOWORK=off CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags "-X fluxagent/internal/version.Version=${VERSION} -X fluxagent/internal/version.GitCommit=${GIT_COMMIT} -X fluxagent/internal/version.GitDirty=${GIT_DIRTY} -X fluxagent/internal/version.BuildDate=${BUILD_DATE}" -o /out/fluxagent-operator ./cmd/operator \
    && touch -d "@${SOURCE_DATE_EPOCH}" /out/fluxagent-operator

FROM gcr.io/distroless/static:nonroot@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6

ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG GIT_DIRTY=unknown
ARG BUILD_DATE=unknown
ARG SOURCE_DATE_EPOCH=0

LABEL org.opencontainers.image.title="FluxAgent" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      io.fluxagent.git-dirty="${GIT_DIRTY}"

WORKDIR /
COPY --from=builder /out/fluxagent-operator /fluxagent-operator

USER nonroot:nonroot
ENTRYPOINT ["/fluxagent-operator"]
