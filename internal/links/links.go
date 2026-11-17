package links

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type Kind string

const (
	KindFile Kind = "file"
	KindURL  Kind = "web"
	KindDir  Kind = "dir"
	KindJira Kind = "jira"
)

type Item struct {
	Kind       Kind
	Raw        string
	Target     string
	Line       int
	EndLine    int
	Title      string
	Column     int
	SourceLine string
}

var (
	jiraIssueRE = regexp.MustCompile(`[A-Z][A-Z0-9_]*-[0-9]+`)
)

// Extract uses non-overlapping URL/quoted/path tokens so URL components never
// become spurious local references. Only existing scrollback paths are offered.
func Extract(scrollback string, baseDir string, urlSchemes []string, jiraBaseURL string) []Item {
	var items []Item
	add := func(raw, source string, local bool) {
		item, err := Resolve(raw, baseDir, false)
		if !strings.HasPrefix(strings.ToLower(raw), "file:") && validURL(raw, urlSchemes) {
			item = Item{Raw: raw, Target: raw, Kind: Classify(raw)}
			err = nil
		}
		if err != nil {
			return
		}
		if local || item.Kind == KindFile || item.Kind == KindDir {
			info, err := os.Stat(item.Target)
			if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
				return
			}
		} else if !validURL(item.Target, urlSchemes) {
			return
		}
		item.SourceLine = source
		items = append(items, item)
	}
	schemes := append(append([]string{}, urlSchemes...), "file")
	urlRE := urlRefRegexp(schemes)
	pattern := quotedRefRE.String()
	if urlRE != nil {
		pattern += "|" + urlRE.String()
	}
	tokenRE := regexp.MustCompile(pattern)
	for _, line := range strings.Split(ansi.Strip(scrollback), "\n") {
		// A whole-line path may contain spaces without quoting.
		if item, err := Resolve(strings.TrimSpace(line), baseDir, false); err == nil {
			if info, err := os.Stat(item.Target); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
				add(strings.TrimSpace(line), line, true)
				continue
			}
		}
		masked := []byte(line)
		consume := func(a, b int) {
			for i := a; i < b; i++ {
				masked[i] = ' '
			}
		}
		for _, m := range tokenRE.FindAllStringIndex(line, -1) {
			raw := line[m[0]:m[1]]
			quoted := strings.ContainsAny(raw[:1], "\"'`<")
			if quoted {
				raw = raw[1 : len(raw)-1]
			} else {
				if m[0] > 0 && strings.ContainsAny(line[m[0]-1:m[0]], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+.-") {
					continue
				}
				raw = trimURL(raw)
			}
			add(raw, line, false)
			consume(m[0], m[1])
		}
		for _, token := range strings.Fields(string(masked)) {
			raw := strings.TrimRight(strings.TrimLeft(token, "`\"'()[]<>,;"), "`\"'()[]<>,;.")
			raw = strings.TrimSuffix(raw, ":")
			add(raw, line, true)
		}
		if jiraBaseURL != "" {
			for _, issue := range jiraIssues(string(masked)) {
				items = append(items, Item{Kind: KindJira, Raw: issue, Target: strings.TrimRight(jiraBaseURL, "/") + "/browse/" + issue, SourceLine: line})
			}
		}
	}
	return Deduplicate(items)
}

var quotedRefRE = regexp.MustCompile("\"[^\"\n]+\"|'[^'\n]+'|`[^`\n]+`|<[^<>\n]+>")

func jiraIssues(line string) []string {
	issues := make([]string, 0)
	for _, match := range jiraIssueRE.FindAllStringIndex(line, -1) {
		if match[0] > 0 && isIssueWordByte(line[match[0]-1]) {
			continue
		}
		if match[1] < len(line) && isIssueWordByte(line[match[1]]) {
			continue
		}
		issues = append(issues, line[match[0]:match[1]])
	}
	return issues
}

func isIssueWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

func trimURL(s string) string {
	s = strings.TrimRight(s, ".,;`")
	for _, pair := range [][2]string{{"(", ")"}, {"[", "]"}, {"{", "}"}} {
		for strings.HasSuffix(s, pair[1]) && strings.Count(s, pair[1]) > strings.Count(s, pair[0]) {
			s = strings.TrimSuffix(s, pair[1])
		}
	}
	return s
}

func urlRefRegexp(schemes []string) *regexp.Regexp {
	patterns := make([]string, 0, len(schemes))
	for _, scheme := range schemes {
		scheme = strings.TrimSpace(scheme)
		if scheme != "" {
			patterns = append(patterns, regexp.QuoteMeta(scheme))
		}
	}
	if len(patterns) == 0 {
		return nil
	}
	return regexp.MustCompile(`(?i)(?:` + strings.Join(patterns, "|") + `):(?://)?[^\s<>]+`)
}

func validURL(s string, schemes []string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || (u.Host == "" && u.Opaque == "" && u.Path == "") {
		return false
	}
	for _, scheme := range schemes {
		if !strings.EqualFold(u.Scheme, strings.TrimSpace(scheme)) {
			continue
		}
		if strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https") {
			return u.Host != ""
		}
		return true
	}
	return false
}

func resolvePath(path string, baseDir string) string {
	path = expandHome(path)
	if filepath.IsAbs(path) || baseDir == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + strings.TrimPrefix(path, "~")
		}
	}
	return path
}
