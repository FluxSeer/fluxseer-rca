# Model Gateway

FluxAgent treats model providers as pluggable backends.

Current provider scaffolds:

- `heuristic`
- `openai`
- `claude`
- `gemini`
- `bedrock`
- `local`

Each provider implements:

```go
type Provider interface {
    Name() string
    Complete(ctx context.Context, req domain.ModelRequest) (domain.ModelResponse, error)
}
```

The current runtime still uses a heuristic provider by default so the project remains safe and demoable without secrets.
