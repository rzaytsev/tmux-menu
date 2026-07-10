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
	urlRefRE  = regexp.MustCompile(`https?://[^\s<>"']+`)
	fileRefRE = regexp.MustCompile(
		`(?:^|[\s"'(<\[])(` +
			`(?:~\/|\.{1,2}\/|\/|[A-Za-z0-9_.-]+\/)?` +
			`(?:[A-Za-z0-9_@.+-]+\/)*` +
			`(?:[A-Za-z0-9_@.+-]+\.[A-Za-z0-9][A-Za-z0-9._+-]*|Makefile|Dockerfile)` +
			`(?::\d+(?:-\d+)?)?(?::\d+)?` +
			`)(?:$|[\s"')>\],;:.])`,
	)
)

func Extract(scrollback string, baseDir string) []Item {
	seen := make(map[string]bool)
	items := make([]Item, 0)
	for _, line := range strings.Split(scrollback, "\n") {
		for _, match := range urlRefRE.FindAllString(line, -1) {
			target := trimURL(match)
			if !validURL(target) {
				continue
			}
			key := string(KindURL) + "\x00" + target
			if seen[key] {
				continue
			}
			seen[key] = true
			items = append(items, Item{Kind: KindURL, Raw: target, Target: target, SourceLine: strings.TrimSpace(line)})
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

func trimURL(s string) string {
	return strings.TrimRight(s, ".,;)]}")
}

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme != "" && u.Host != ""
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
