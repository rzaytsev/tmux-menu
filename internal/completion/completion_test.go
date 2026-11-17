package completion

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func fixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	base := []string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-C", root}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFixture(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func values(t *testing.T, dir, provider string) []string {
	t.Helper()
	listing, err := List(context.Background(), dir, provider)
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for _, entry := range listing.Entries {
		result = append(result, entry.Value)
	}
	sort.Strings(result)
	return result
}

func TestGitFilesAndChangedRespectCwdAndRawPaths(t *testing.T) {
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q", "-b", "main")
	for name, body := range map[string]string{
		".gitignore": "ignored*\n", "keep.go": "keep", "gone.go": "gone", "old name.go": "rename",
		"sub/tracked.go": "nested", "other/outside.go": "sibling",
	} {
		writeFixture(t, root, name, body)
	}
	fixtureGit(t, root, "add", ".")
	fixtureGit(t, root, "commit", "-qm", "initial")
	fixtureGit(t, root, "mv", "old name.go", "new name.go")
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "sub/tracked.go", "modified")
	writeFixture(t, root, "untracked '$(literal).go", "new")
	writeFixture(t, root, "ignored-secret", "ignored")
	writeFixture(t, root, "bad\nfile", "control")
	writeFixture(t, root, "sub/new.go", "untracked")
	files := values(t, root, "files")
	want := []string{".gitignore", "keep.go", "new name.go", "other/outside.go", "sub/new.go", "sub/tracked.go", "untracked '$(literal).go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %#v, want %#v", files, want)
	}
	listing, err := List(context.Background(), root, "files")
	if err != nil || !strings.Contains(listing.Note, "omitted") {
		t.Fatalf("control omission not visible: %#v, %v", listing, err)
	}
	changed := values(t, root, "changed")
	want = []string{"gone.go", "new name.go", "sub/new.go", "sub/tracked.go", "untracked '$(literal).go"}
	if !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %#v, want %#v", changed, want)
	}
	for _, provider := range []string{"files", "changed"} {
		if got := values(t, filepath.Join(root, "sub"), provider); !reflect.DeepEqual(got, []string{"new.go", "tracked.go"}) {
			t.Fatalf("nested %s = %#v", provider, got)
		}
	}
}

func TestGitObjectsUnbornDetachedAndWorktrees(t *testing.T) {
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q", "-b", "main")
	writeFixture(t, root, "new.go", "new")
	for _, provider := range []string{"branches", "tags", "commits"} {
		if got := values(t, root, provider); len(got) != 0 {
			t.Fatalf("unborn %s = %#v", provider, got)
		}
	}
	if got := values(t, root, "changed"); !reflect.DeepEqual(got, []string{"new.go"}) {
		t.Fatal(got)
	}
	fixtureGit(t, root, "add", ".")
	fixtureGit(t, root, "commit", "-qm", "initial subject")
	id := fixtureGit(t, root, "rev-parse", "HEAD")
	fixtureGit(t, root, "branch", "feature")
	fixtureGit(t, root, "tag", "v1")
	fixtureGit(t, root, "update-ref", "refs/remotes/origin/main", id)
	fixtureGit(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	if got := values(t, root, "branches"); !reflect.DeepEqual(got, []string{"feature", "main", "origin/main"}) {
		t.Fatal(got)
	}
	if got := values(t, root, "tags"); !reflect.DeepEqual(got, []string{"v1"}) {
		t.Fatal(got)
	}
	fixtureGit(t, root, "checkout", "--detach", "-q")
	if got := values(t, root, "commits"); !reflect.DeepEqual(got, []string{id}) {
		t.Fatal(got)
	}
	other := filepath.Join(t.TempDir(), "work tree")
	fixtureGit(t, root, "worktree", "add", "-q", other, "feature")
	want := []string{root, other}
	for i, path := range want {
		want[i], _ = filepath.EvalSymlinks(path)
	}
	sort.Strings(want)
	if got := values(t, root, "worktrees"); !reflect.DeepEqual(got, want) {
		t.Fatalf("worktrees = %#v, want %#v", got, want)
	}
	// Detached HEAD stays visible even without a branch naming its new commit.
	writeFixture(t, root, "new.go", "detached")
	fixtureGit(t, root, "commit", "-qam", "detached subject")
	if got := values(t, root, "commits"); len(got) != 2 {
		t.Fatalf("detached commits = %#v", got)
	}
	fixtureGit(t, root, "symbolic-ref", "HEAD", "refs/heads/unborn")
	if got := values(t, root, "commits"); len(got) != 1 || got[0] != id {
		t.Fatalf("refs without a born HEAD = %#v", got)
	}
}

func TestWalkIncludesEmptyAndHiddenDirsWithoutFollowingSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"empty", ".hidden", "node_modules/skip", ".git/skip"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(t, root, "file space.txt", "file")
	outside := t.TempDir()
	writeFixture(t, outside, "external", "not listed")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	// Use walk directly: the deliberately present .git directory is not a repo.
	listing, err := walk(context.Background(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, entry := range listing.Entries {
		dirs = append(dirs, entry.Value)
	}
	if !reflect.DeepEqual(dirs, []string{".hidden", "empty"}) {
		t.Fatal(dirs)
	}
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := values(t, root, "files"); !reflect.DeepEqual(got, []string{"file space.txt"}) {
		t.Fatal(got)
	}
	if got := values(t, root, "branches"); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestBoundsAndControlValues(t *testing.T) {
	for _, value := range []string{"", "a\nb", "a\tb", "a\x1b[31mb", "x\u202ey", string([]byte{0xff})} {
		if SafeValue(value) {
			t.Fatalf("accepted unsafe %q", value)
		}
	}
	if !SafeValue("路径 'literal' $(nothing)") {
		t.Fatal("rejected printable text")
	}
	buffer := limitedBuffer{limit: 3}
	if _, err := buffer.Write([]byte("four")); err == nil || !buffer.overflow || buffer.Len() != 0 {
		t.Fatal("output cap failed")
	}
	buffer = limitedBuffer{limit: 3}
	if _, err := io.Copy(&buffer, io.LimitReader(strings.NewReader("four"), 4)); err == nil || !buffer.overflow {
		t.Fatal("io.Copy bypassed the output cap")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := List(ctx, t.TempDir(), "files"); err == nil {
		t.Fatal("cancelled Git probe was hidden")
	}
	entries := make([]Entry, MaxItems+1)
	for i := range entries {
		entries[i] = Entry{Value: fmt.Sprintf("path-%d", i)}
	}
	result := finish(entries)
	if len(result.Entries) != MaxItems || !strings.Contains(result.Note, "limit") {
		t.Fatalf("row cap: %d %s", len(result.Entries), result.Note)
	}
}
