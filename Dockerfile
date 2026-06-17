FROM golang:1.26.2 AS builder

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/fluxagent-operator ./cmd/operator

FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /out/fluxagent-operator /fluxagent-operator

USER nonroot:nonroot
ENTRYPOINT ["/fluxagent-operator"]
