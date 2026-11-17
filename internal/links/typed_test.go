package links

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestServiceClassification(t *testing.T) {
	for target, want := range map[string]Kind{
		"https://example.com": "web", "slack://channel?id=one": "slack", "https://team.slack.com/a": "slack",
		"https://org.atlassian.net/browse/OPS-1?q=2": "jira", "https://org.atlassian.net/wiki": "web",
		"https://app.notion.com/a": "notion", "https://www.notion.so/a": "notion",
		"https://github.com/org/repo": "gh", "https://github.com/org/repo/pull/12/files": "pr",
		"https://github.com/org/repo/issues/3": "issue", "https://github.com/org/repo/actions/runs/123": "ci",
		"https://github.com/org/repo/commit/abc123": "commit", "https://github.com/org/repo/releases/tag/v1": "release",
		"https://console.cloud.google.com/iam": "GCP", "https://us-east-1.console.aws.amazon.com/ec2": "AWS",
		"https://console.aws.amazon.com/": "AWS", "https://github.com.evil.test/o/r/pull/1": "web",
		"https://evilgithub.com/o/r": "web", "https://github.com@evil.test/o/r": "web",
		"https://slack.com.evil.test/": "web", "https://evilnotion.com/": "web",
		"https://console.cloud.google.com.evil.test/": "web", "https://console.aws.amazon.com.evil.test/": "web",
		"https://evilconsole.aws.amazon.com/": "web", "https://atlassian.net.evil.test/browse/OPS-1": "web",
	} {
		if got := Classify(target); got != want {
			t.Errorf("%s = %s, want %s", target, got, want)
		}
	}
}
func TestLocationsAndLiteralPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	mustWrite(t, filepath.Join(root, "design notes.md"))
	os.Mkdir(filepath.Join(root, "dir space"), 0700)
	for _, tc := range []struct {
		raw            string
		line, end, col int
		kind           Kind
	}{
		{"design notes.md", 0, 0, 0, KindFile}, {"design notes.md:5", 5, 0, 0, KindFile},
		{"design notes.md:5-10", 5, 10, 0, KindFile}, {"design notes.md:5:12", 5, 0, 12, KindFile},
		{"design notes.md#L5", 5, 0, 0, KindFile}, {"design notes.md#L5-L10", 5, 10, 0, KindFile},
		{"file://" + root + "/design%20notes.md#L5-L10", 5, 10, 0, KindFile}, {"~/design notes.md", 0, 0, 0, KindFile},
		{"dir space/", 0, 0, 0, KindDir},
	} {
		got, err := Resolve(tc.raw, root, true)
		if err != nil {
			t.Fatal(tc.raw, err)
		}
		if got.Line != tc.line || got.EndLine != tc.end || got.Column != tc.col || got.Kind != tc.kind {
			t.Errorf("%s: %#v", tc.raw, got)
		}
	}
	for _, raw := range []string{"x.md:0", "x.md:10-5", "x.md:9999999999", "file://remote/a", "x\x1b.md", "javascript:alert(1)", "x%00.md"} {
		if _, err := Resolve(raw, root, true); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	raw := "literal $HOME;$(touch marker)&'().md"
	got, err := Resolve(raw, root, false)
	if err != nil || got.Target != filepath.Join(root, raw) {
		t.Fatalf("literal path changed: %#v %v", got, err)
	}
	url := "https://example.com/a(b)?q=$(echo)&x='literal';#frag"
	got, err = Resolve(url, root, true)
	if err != nil || got.Target != url {
		t.Fatalf("URL changed: %#v %v", got, err)
	}
}
func TestExtractSpacesRangesAndBalancedURLs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design notes.md"))
	os.Mkdir(filepath.Join(root, "some dir"), 0700)
	input := "see `design notes.md:5-10` and (https://example.com/a(b)?q=$(echo)&x=1#frag)\n\"some dir/\"\nfile://" + root + "/design%20notes.md:5-10\n"
	items := Extract(input, root, defaultURLSchemes, "")
	if len(items) != 3 {
		t.Fatalf("items: %#v", items)
	}
	if items[0].EndLine != 10 || items[1].Target != "https://example.com/a(b)?q=$(echo)&x=1#frag" || items[2].Kind != KindDir {
		t.Fatalf("items: %#v", items)
	}
}
func TestCommonMarkOnlyActualLinks(t *testing.T) {
	source := []byte("[Inline](https://example.com/a(b)?x=1&y=2#frag)\n[Reference][ref]\n\n[ref]: <design notes.md#L5-L10>\n\n<https://github.com/org/repo/pull/1>\n`[code](https://inline.invalid)`\n\n```md\n[code](https://fenced.invalid)\n```\n\n    [code](https://indented.invalid)\n\n![image](https://image.invalid)\n[![alt](img.png)](https://outer.example)\n[Space](<design notes.md:5:12>)\nraw https://bare.invalid\n[ref]\n")
	got := Markdown(source)
	var targets []string
	for _, item := range got {
		targets = append(targets, item.Target)
	}
	want := []string{"https://example.com/a(b)?x=1&y=2#frag", "design notes.md#L5-L10", "https://github.com/org/repo/pull/1", "https://outer.example", "design notes.md:5:12", "design notes.md#L5-L10"}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets %#v", targets)
	}
}
func TestDedupResolvedLocationsKeepsUsefulTitle(t *testing.T) {
	var items []Item
	for _, raw := range []string{"a.md#L5-L10", "./a.md:5-10"} {
		item, err := Resolve(raw, "/workspace", true)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	items[1].Title = "Useful title"
	got := Deduplicate(items)
	if len(got) != 1 || got[0].Title != "Useful title" {
		t.Fatal(got)
	}
}

func TestMarkdownEncodedLiteralFilenameCharacters(t *testing.T) {
	for raw, want := range map[string]string{
		"hash%23name.md#heading":              "/workspace/hash#name.md",
		"query%3Fname.md?view=1":              "/workspace/query?name.md",
		"colon%3A5.md":                        "/workspace/colon:5.md",
		"file:///workspace/hash%23name.md#L5": "/workspace/hash#name.md",
	} {
		got, err := Resolve(raw, "/workspace", true)
		if err != nil || got.Target != want {
			t.Errorf("%s = %#v %v", raw, got, err)
		}
	}
}

func TestTypedResultsOmitFilesystemRoot(t *testing.T) {
	root := t.TempDir()
	for _, input := range []string{"/", "`/`", "file:///", "//", root + "/.."} {
		items := Extract(input, root, defaultURLSchemes, "")
		for _, item := range items {
			if item.Kind == KindDir && item.Target == "/" {
				t.Fatalf("root offered for %q: %#v", input, items)
			}
		}
	}
	// The shared result boundary also excludes explicit Markdown root links.
	items := Deduplicate([]Item{{Kind: KindDir, Target: "/"}, {Kind: KindDir, Target: root}, {Kind: KindURL, Target: "https://example.com/"}})
	if len(items) != 2 || items[0].Target != root || items[1].Target != "https://example.com/" {
		t.Fatalf("unexpected results: %#v", items)
	}
}
