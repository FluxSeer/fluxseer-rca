package notifier

import "context"

type Message struct {
	Title   string         `json:"title"`
	Summary string         `json:"summary"`
	Body    string         `json:"body"`
	Fields  map[string]any `json:"fields,omitempty"`
}

type Notifier interface {
	Name() string
	Notify(ctx context.Context, message Message) error
}
