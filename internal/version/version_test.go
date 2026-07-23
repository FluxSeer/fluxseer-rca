package version

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteText(t *testing.T) {
	orig := Current()
	t.Cleanup(func() {
		Version = orig.Version
		GitCommit = orig.GitCommit
		GitDirty = orig.GitDirty
		BuildDate = orig.BuildDate
	})
	Version = "v0.2.0-beta.1"
	GitCommit = "0123456789abcdef0123456789abcdef01234567"
	GitDirty = "false"
	BuildDate = "2026-07-23T00:00:00Z"

	var buf bytes.Buffer
	if err := Write(&buf, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Version: v0.2.0-beta.1",
		"GitCommit: 0123456789abcdef0123456789abcdef01234567",
		"GitDirty: false",
		"BuildDate: 2026-07-23T00:00:00Z",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got %q", want, out)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	orig := Current()
	t.Cleanup(func() {
		Version = orig.Version
		GitCommit = orig.GitCommit
		GitDirty = orig.GitDirty
		BuildDate = orig.BuildDate
	})
	Version = "v0.2.0-beta.1"
	GitCommit = "0123456789abcdef0123456789abcdef01234567"
	GitDirty = "false"
	BuildDate = "2026-07-23T00:00:00Z"

	var buf bytes.Buffer
	if err := Write(&buf, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"version":"v0.2.0-beta.1","gitCommit":"0123456789abcdef0123456789abcdef01234567","gitDirty":"false","buildDate":"2026-07-23T00:00:00Z"}`
	if strings.TrimSpace(buf.String()) != want {
		t.Fatalf("expected %s, got %s", want, strings.TrimSpace(buf.String()))
	}
}

func TestParseOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "default", args: nil, want: "text"},
		{name: "json equals", args: []string{"--output=json"}, want: "json"},
		{name: "json split", args: []string{"--output", "json"}, want: "json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOutput(tc.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, got)
			}
		})
	}
}
