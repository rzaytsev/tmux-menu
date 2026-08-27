package links

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Kind string

const (
	KindFile Kind = "file"
	KindURL  Kind = "url"
	KindJira Kind = "jira"
)

type Item struct {
	Kind       Kind
	Raw        string
	Target     string
	Line       int
	Column     int
	SourceLine string
}

var (
	fileRefRE = regexp.MustCompile(
		`(?:^|[\s"'(<\[])(` +
			`(?:~\/|\.{1,2}\/|\/|[A-Za-z0-9_.-]+\/)?` +
			`(?:[A-Za-z0-9_@.+-]+\/)*` +
			`(?:[A-Za-z0-9_@.+-]+\.[A-Za-z0-9][A-Za-z0-9._+-]*|Makefile|Dockerfile)` +
			`(?::\d+(?:-\d+)?)?(?::\d+)?` +
			`)(?:$|[\s"')>\],;:.])`,
	)
	jiraIssueRE = regexp.MustCompile(`[A-Z][A-Z0-9_]*-[0-9]+`)
)

func Extract(scrollback string, baseDir string, urlSchemes []string, jiraBaseURL string) []Item {
	seen := make(map[string]bool)
	items := make([]Item, 0)
	urlRefRE := urlRefRegexp(urlSchemes)
	jiraBaseURL = strings.TrimRight(strings.TrimSpace(jiraBaseURL), "/")
	for _, line := range strings.Split(scrollback, "\n") {
		if urlRefRE != nil {
			for _, match := range urlRefRE.FindAllStringSubmatch(line, -1) {
				target := trimURL(match[1])
				if !validURL(target, urlSchemes) {
					continue
				}
				kind := KindURL
				raw := target
				if issue, ok := jiraIssueFromURL(target, jiraBaseURL); ok {
					kind = KindJira
					raw = issue
					target = jiraBaseURL + "/browse/" + issue
				}
				key := string(kind) + "\x00" + target
				if seen[key] {
					continue
				}
				seen[key] = true
				items = append(items, Item{Kind: kind, Raw: raw, Target: target, SourceLine: strings.TrimSpace(line)})
			}
		}
		if jiraBaseURL != "" {
			for _, issue := range jiraIssues(line) {
				target := jiraBaseURL + "/browse/" + issue
				key := string(KindJira) + "\x00" + target
				if seen[key] {
					continue
				}
				seen[key] = true
				items = append(items, Item{Kind: KindJira, Raw: issue, Target: target, SourceLine: strings.TrimSpace(line)})
			}
		}

		for _, match := range fileRefRE.FindAllStringSubmatch(line, -1) {
			if len(match) < 2 {
				continue
			}
			raw := trimFileRef(match[1])
			if strings.Contains(raw, "://") {
				continue
			}
			path, lineNo, colNo := splitLocation(raw)
			target := resolvePath(path, baseDir)
			info, err := os.Stat(target)
			if err != nil || info.IsDir() {
				continue
			}
			key := string(KindFile) + "\x00" + target + "\x00" + strconv.Itoa(lineNo) + "\x00" + strconv.Itoa(colNo)
			if seen[key] {
				continue
			}
			seen[key] = true
			items = append(items, Item{
				Kind:       KindFile,
				Raw:        raw,
				Target:     target,
				Line:       lineNo,
				Column:     colNo,
				SourceLine: strings.TrimSpace(line),
			})
		}
	}
	return items
}

func jiraIssueFromURL(target string, jiraBaseURL string) (string, bool) {
	prefix := jiraBaseURL + "/browse/"
	if jiraBaseURL == "" || !strings.HasPrefix(target, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(target, prefix)
	match := jiraIssueRE.FindStringIndex(rest)
	if match == nil || match[0] != 0 || match[1] < len(rest) && isIssueWordByte(rest[match[1]]) {
		return "", false
	}
	return rest[:match[1]], true
}

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
	return strings.TrimRight(s, ".,;)]}`")
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
	return regexp.MustCompile(`(?i)(?:^|[^a-z0-9+.-])((?:` + strings.Join(patterns, "|") + `):(?://)?[^\s<>"']+)`)
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

func trimFileRef(s string) string {
	return strings.TrimRight(strings.Trim(s, `"'()[]<>,;`), ".")
}

func splitLocation(raw string) (string, int, int) {
	parts := strings.Split(raw, ":")
	if len(parts) == 1 {
		return raw, 0, 0
	}

	col := 0
	last := parts[len(parts)-1]
	if n, ok := parsePositiveInt(last); ok {
		col = n
		parts = parts[:len(parts)-1]
	}

	line := 0
	if len(parts) > 1 {
		last = parts[len(parts)-1]
		if n, ok := parseLineStart(last); ok {
			line = n
			parts = parts[:len(parts)-1]
		} else if col > 0 {
			line = col
			col = 0
		}
	} else if col > 0 {
		line = col
		col = 0
	}

	return strings.Join(parts, ":"), line, col
}

func parseLineStart(s string) (int, bool) {
	start, _, _ := strings.Cut(s, "-")
	return parsePositiveInt(start)
}

func parsePositiveInt(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
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
	return os.ExpandEnv(path)
}
