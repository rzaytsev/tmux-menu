package picker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Filter uses the same fzf matching rules on full fields before presentation.
// Output is a subset of bounded input, in fzf rank or original input order.
func Filter(ctx context.Context, input, query, fields string, preserveOrder bool) (string, error) {
	if query == "" || input == "" {
		return input, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	args := []string{"--filter", query, "--ansi", "--delimiter", "\t", "--with-nth", fields}
	if preserveOrder {
		args = append(args, "--no-sort")
	}
	cmd := exec.CommandContext(ctx, "fzf", args...)
	cmd.Env = append(os.Environ(), "FZF_DEFAULT_OPTS=", "FZF_DEFAULT_OPTS_FILE=")
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return "", fmt.Errorf("match picker query: %w", err)
	}
	return string(output), nil
}
