FROM golang:1.26.2 AS builder

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/demo-observability ./cmd/demo-observability

FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /out/demo-observability /demo-observability

USER nonroot:nonroot
ENTRYPOINT ["/demo-observability"]
