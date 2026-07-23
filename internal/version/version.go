package version

import (
	"encoding/json"
	"fmt"
	"io"
)

var (
	Version   = "dev"
	GitCommit = "unknown"
	GitDirty  = "unknown"
	BuildDate = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	GitDirty  string `json:"gitDirty"`
	BuildDate string `json:"buildDate"`
}

func Current() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		GitDirty:  GitDirty,
		BuildDate: BuildDate,
	}
}

func Write(w io.Writer, output string) error {
	info := Current()
	switch output {
	case "json":
		encoder := json.NewEncoder(w)
		return encoder.Encode(info)
	case "", "text":
		_, err := fmt.Fprintf(w, "Version: %s\nGitCommit: %s\nGitDirty: %s\nBuildDate: %s\n", info.Version, info.GitCommit, info.GitDirty, info.BuildDate)
		return err
	default:
		return fmt.Errorf("unsupported version output %q", output)
	}
}

func ParseOutput(args []string) (string, error) {
	output := "text"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output=json":
			output = "json"
		case "--output=text":
			output = "text"
		case "--output":
			if i+1 >= len(args) {
				return "", fmt.Errorf("--output requires a value")
			}
			i++
			output = args[i]
		default:
			return "", fmt.Errorf("unknown version flag %q", args[i])
		}
	}
	if output != "json" && output != "text" {
		return "", fmt.Errorf("unsupported version output %q", output)
	}
	return output, nil
}
