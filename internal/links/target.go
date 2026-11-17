package links

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Classify uses parsed hostnames, never substring matches on titles or URLs.
func Classify(target string) Kind {
	u, err := url.Parse(target)
	if err != nil {
		return KindURL
	}
	if strings.EqualFold(u.Scheme, "slack") {
		return "slack"
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return KindURL
	}
	host := strings.ToLower(u.Hostname())
	domain := func(d string) bool { return host == d || strings.HasSuffix(host, "."+d) }
	switch {
	case host == "console.cloud.google.com":
		return "GCP"
	case domain("console.aws.amazon.com"):
		return "AWS"
	case domain("slack.com"):
		return "slack"
	case domain("notion.com") || domain("notion.so"):
		return "notion"
	case domain("atlassian.net") && jiraPathRE.MatchString(u.Path):
		return KindJira
	case host == "github.com":
		for _, rule := range githubRules {
			if rule.re.MatchString(u.Path) {
				return rule.kind
			}
		}
	}
	return KindURL
}

var jiraPathRE = regexp.MustCompile(`^/browse/[A-Z][A-Z0-9_]*-[0-9]+(?:/|$)`)
var githubRules = []struct {
	kind Kind
	re   *regexp.Regexp
}{
	{"pr", regexp.MustCompile(`^/[^/]+/[^/]+/pull/[0-9]+(?:/|$)`)},
	{"issue", regexp.MustCompile(`^/[^/]+/[^/]+/issues/[0-9]+(?:/|$)`)},
	{"ci", regexp.MustCompile(`^/[^/]+/[^/]+/actions/runs/[0-9]+(?:/|$)`)},
	{"commit", regexp.MustCompile(`^/[^/]+/[^/]+/commit/[a-fA-F0-9]+(?:/|$)`)},
	{"release", regexp.MustCompile(`^/[^/]+/[^/]+/releases(?:/|$)`)},
	{"gh", regexp.MustCompile(`^/[^/]+/[^/]+(?:/|$)`)},
}
var locationRE = regexp.MustCompile(`(?::([0-9]+)(?::([0-9]+)|-([0-9]+))?|#L([0-9]+)(?:-L?([0-9]+))?)$`)
var schemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// Resolve keeps the physical path separate from its navigation suffix. Markdown
// URI decoding never expands environment variables or evaluates shell syntax.
func Resolve(raw, base string, markdown bool) (Item, error) {
	item := Item{Raw: raw}
	if raw == "" || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) }) {
		return item, fmt.Errorf("empty or control-bearing target")
	}
	path := raw
	if strings.HasPrefix(path, "//") && markdown {
		path = "https:" + path
	}
	u, parseErr := url.Parse(path)
	if parseErr == nil && u.Scheme != "" && !strings.EqualFold(u.Scheme, "file") && (strings.Contains(path, "://") || !locationRE.MatchString(path)) {
		if !validURL(path, []string{"http", "https", "slack", "tg", "mailto", "ftp"}) {
			return item, fmt.Errorf("unsupported target scheme")
		}
		item.Target = path
		item.Kind = Classify(path)
		return item, nil
	}
	fileURL := strings.HasPrefix(strings.ToLower(path), "file:")
	if fileURL {
		if parseErr != nil || u.Opaque != "" || (u.Host != "" && u.Host != "localhost") {
			return item, fmt.Errorf("nonlocal file URL")
		}
		path = u.EscapedPath()
		if u.Fragment != "" {
			path += "#" + u.EscapedFragment()
		}
	}
	if markdown || fileURL {
		var fragment string
		path, fragment, _ = strings.Cut(path, "#")
		path, _, _ = strings.Cut(path, "?")
		if decoded, err := url.PathUnescape(fragment); err == nil && strings.HasPrefix(decoded, "L") && locationRE.MatchString("#"+decoded) {
			path += "#" + decoded
		}
	}
	if m := locationRE.FindStringSubmatchIndex(path); m != nil {
		match := locationRE.FindStringSubmatch(path)
		number := func(s string) (int, error) {
			if s == "" {
				return 0, nil
			}
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || len(s) > 9 {
				return 0, fmt.Errorf("invalid location")
			}
			return n, nil
		}
		var err error
		start := match[1]
		if start == "" {
			start = match[4]
		}
		if item.Line, err = number(start); err != nil {
			return item, err
		}
		if item.Column, err = number(match[2]); err != nil {
			return item, err
		}
		end := match[3]
		if end == "" {
			end = match[5]
		}
		if item.EndLine, err = number(end); err != nil {
			return item, err
		}
		if item.EndLine > 0 && item.EndLine < item.Line {
			return item, fmt.Errorf("invalid range")
		}
		path = path[:m[0]]
	}
	if path == "" || strings.ContainsFunc(path, unicode.IsControl) || schemeRE.MatchString(path) {
		return item, fmt.Errorf("invalid local path")
	}
	if markdown || fileURL {
		decoded, err := url.PathUnescape(path)
		if err != nil {
			return item, err
		}
		path = decoded
		if strings.ContainsFunc(path, unicode.IsControl) {
			return item, fmt.Errorf("control-bearing decoded path")
		}
	}
	item.Target = resolvePath(path, base)
	item.Kind = KindFile
	if info, err := os.Stat(item.Target); err == nil && info.IsDir() {
		item.Kind = KindDir
	}
	return item, nil
}

func (i Item) CopyTarget() string {
	target := i.Target
	if i.Line > 0 {
		target += fmt.Sprintf(":%d", i.Line)
		if i.EndLine > i.Line {
			target += fmt.Sprintf("-%d", i.EndLine)
		} else if i.Column > 0 {
			target += fmt.Sprintf(":%d", i.Column)
		}
	}
	return target
}
func Deduplicate(items []Item) []Item {
	seen := map[string]int{}
	out := make([]Item, 0, len(items))
	for _, item := range items {
		if item.Kind == KindDir && item.Target == "/" {
			continue
		}
		key := item.CopyTarget()
		if idx, ok := seen[key]; ok {
			if (out[idx].Title == "" || out[idx].Title == out[idx].Raw) && item.Title != "" {
				out[idx].Title = item.Title
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, item)
	}
	return out
}
