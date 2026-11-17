// Package completion lists literal context references without executing actions.
package completion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"tmux-menu/internal/displaytext"
)

const MaxItems = 20000
const MaxBytes = 8 << 20

var Providers = []string{"files", "dirs", "changed", "branches", "tags", "commits", "worktrees", "screen"}

type Entry struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Detail string `json:"detail,omitempty"`
	Root   string `json:"root,omitempty"`
	Path   string `json:"path,omitempty"`
	Object string `json:"object,omitempty"`
}

type Listing struct {
	Entries []Entry
	Note    string
}

func ValidProvider(provider string) bool {
	for _, p := range Providers {
		if p == provider {
			return true
		}
	}
	return false
}

func SafeValue(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.ContainsFunc(value, displaytext.UnsafeControl)
}

// Git runs bounded read commands with no pager, optional index writes or fsmonitor.
// Callers use fixed subcommands and separate paths/revisions from options.
func Git(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	base := []string{"--no-pager", "--no-optional-locks", "--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "color.ui=false", "-C", root}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	out := &limitedBuffer{limit: MaxBytes}
	errout := &limitedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = out, errout
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	if out.overflow || errout.overflow {
		return "", fmt.Errorf("Git output exceeds limit")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errout.String()))
	}
	return out.String(), nil
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Len() int       { return b.buffer.Len() }
func (b *limitedBuffer) String() string { return b.buffer.String() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.overflow = true
		return 0, fmt.Errorf("output exceeds limit")
	}
	return b.buffer.Write(p)
}

// List is rooted at the invoking pane cwd. Git objects use its enclosing repo.
func List(ctx context.Context, dir, provider string) (Listing, error) {
	if !ValidProvider(provider) || provider == "screen" {
		return Listing{}, fmt.Errorf("unsupported completion provider %q", provider)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Listing{}, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return Listing{}, err
	}
	if provider == "dirs" {
		return walk(ctx, dir, true)
	}
	// Distinguish an ordinary non-repository from a failed Git read.
	probe, err := Git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if ctx.Err() != nil || !strings.Contains(err.Error(), "not a git repository") {
			return Listing{}, err
		}
		if provider == "files" {
			return walk(ctx, dir, false)
		}
		return Listing{Note: "Not in a Git worktree"}, nil
	}
	if strings.TrimSpace(probe) != "true" {
		return Listing{Note: "Not in a Git worktree"}, nil
	}
	var entries []Entry
	switch provider {
	case "files":
		out, err := Git(ctx, dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", ".")
		if err != nil {
			return Listing{}, err
		}
		for _, value := range strings.Split(out, "\x00") {
			path := filepath.Join(dir, value)
			if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
				entries = append(entries, Entry{Kind: "file", Value: value, Path: path})
			}
		}
	case "changed":
		root, err := Git(ctx, dir, "rev-parse", "--show-toplevel")
		if err != nil {
			return Listing{}, err
		}
		out, err := Git(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", ".")
		if err != nil {
			return Listing{}, err
		}
		entries = changedEntries(dir, strings.TrimSuffix(root, "\n"), out)
	case "branches", "tags":
		refs := []string{"refs/heads", "refs/remotes"}
		if provider == "tags" {
			refs = []string{"refs/tags"}
		}
		args := append([]string{"for-each-ref", "--count=20001", "--format=%(refname)%00%(objectname)%00%(symref)%00%(subject)"}, refs...)
		out, err := Git(ctx, dir, args...)
		if err != nil {
			return Listing{}, err
		}
		for _, line := range strings.Split(out, "\n") {
			parts := strings.SplitN(line, "\x00", 4)
			if len(parts) != 4 || parts[2] != "" {
				continue
			}
			value := parts[0]
			for _, prefix := range []string{"refs/heads/", "refs/remotes/", "refs/tags/"} {
				value = strings.TrimPrefix(value, prefix)
			}
			kind := "branch"
			if provider == "tags" {
				kind = "tag"
			}
			entries = append(entries, Entry{Kind: kind, Value: value, Detail: parts[3], Root: dir, Object: parts[1]})
		}
	case "commits":
		_, headErr := Git(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD")
		if headErr != nil {
			var exit *exec.ExitError
			if !errors.As(headErr, &exit) || exit.ExitCode() != 1 {
				return Listing{}, headErr
			}
		}
		args := []string{"log", "--all", "--max-count=500", "--format=%H%x00%s"}
		if headErr == nil {
			args = append(args, "HEAD") // Include detached commits, too.
		}
		out, err := Git(ctx, dir, append(args, "--")...)
		if err != nil {
			return Listing{}, err
		}
		for _, line := range strings.Split(out, "\n") {
			id, subject, ok := strings.Cut(line, "\x00")
			if ok {
				entries = append(entries, Entry{Kind: "commit", Value: id, Detail: subject, Root: dir, Object: id})
			}
		}
	case "worktrees":
		out, err := Git(ctx, dir, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			return Listing{}, err
		}
		for _, field := range strings.Split(out, "\x00") {
			if path, ok := strings.CutPrefix(field, "worktree "); ok {
				entries = append(entries, Entry{Kind: "dir", Value: path, Path: path})
			}
		}
	}
	listing := finish(entries)
	if provider == "commits" && len(entries) == 0 {
		listing.Note = "No commits yet"
	}
	if provider == "files" {
		listing.Note = joinNote(listing.Note, "Tracked + untracked; Git ignores apply")
	}
	if provider == "commits" && len(entries) == 500 {
		listing.Note = joinNote(listing.Note, "Latest 500 commits across refs and HEAD")
	}
	return listing, nil
}

func changedEntries(dir, root, out string) []Entry {
	var entries []Entry
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		if len(record) < 4 {
			continue
		}
		status, name := record[:2], record[3:]
		if strings.ContainsAny(status, "RC") {
			i++ // In -z output the destination precedes the old path.
		}
		path := filepath.Join(root, name)
		value, err := filepath.Rel(dir, path)
		if err != nil || value == ".." || strings.HasPrefix(value, ".."+string(os.PathSeparator)) {
			continue
		}
		entries = append(entries, Entry{Kind: "file", Value: value, Detail: status, Path: path})
	}
	return entries
}

var skippedDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true,
	".cache": true, ".next": true, ".venv": true, "venv": true, "__pycache__": true,
	"dist": true, "build": true, "target": true,
}

func walk(ctx context.Context, dir string, dirs bool) (Listing, error) {
	var entries []Entry
	visited := 0
	var note string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			note = "Scan time limit reached; narrow the pane directory"
			return fs.SkipAll
		}
		if err != nil {
			if path == dir {
				return err
			}
			note = "Some paths could not be read"
			return nil
		}
		if path == dir {
			return nil
		}
		visited++
		if visited > 100000 || len(entries) >= MaxItems {
			note = "Scan limit reached; narrow the pane directory"
			return fs.SkipAll
		}
		if d.IsDir() && skippedDirs[d.Name()] {
			return fs.SkipDir
		}
		if (dirs && d.IsDir()) || (!dirs && d.Type().IsRegular()) {
			value, _ := filepath.Rel(dir, path)
			kind := "file"
			if dirs {
				kind = "dir"
			}
			entries = append(entries, Entry{Kind: kind, Value: value, Path: path})
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return Listing{}, err
	}
	result := finish(entries)
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Value < result.Entries[j].Value })
	result.Note = joinNote(result.Note, joinNote(note, "Prunes VCS, dependencies, caches and build dirs; no symlink traversal"))
	return result, nil
}

func finish(entries []Entry) Listing {
	result := Listing{}
	seen := make(map[string]bool)
	for _, entry := range entries {
		if !SafeValue(entry.Value) || (entry.Path != "" && !SafeValue(entry.Path)) {
			result.Note = joinNote(result.Note, "Unsupported control/invalid UTF-8 paths omitted")
			continue
		}
		if seen[entry.Value] {
			continue
		}
		seen[entry.Value] = true
		if len(result.Entries) == MaxItems {
			result.Note = joinNote(result.Note, "Row limit reached")
			break
		}
		result.Entries = append(result.Entries, entry)
	}
	return result
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" || strings.Contains(a, b) {
		return a
	}
	return a + "; " + b
}
