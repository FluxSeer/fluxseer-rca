FROM golang:1.26.2 AS builder

ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG GIT_DIRTY=unknown
ARG BUILD_DATE=unknown

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-X fluxagent/internal/version.Version=${VERSION} -X fluxagent/internal/version.GitCommit=${GIT_COMMIT} -X fluxagent/internal/version.GitDirty=${GIT_DIRTY} -X fluxagent/internal/version.BuildDate=${BUILD_DATE}" -o /out/fluxagent-operator ./cmd/operator

FROM gcr.io/distroless/static:nonroot

ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG GIT_DIRTY=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="FluxAgent" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      io.fluxagent.git-dirty="${GIT_DIRTY}"

WORKDIR /
COPY --from=builder /out/fluxagent-operator /fluxagent-operator

USER nonroot:nonroot
ENTRYPOINT ["/fluxagent-operator"]
