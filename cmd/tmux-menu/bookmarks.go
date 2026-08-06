package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	linkscan "tmux-menu/internal/links"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/shellquote"
)

type bookmarkItem struct {
	Text       string
	Target     string
	SourceFile string
	SourceName string
	Line       int
}

var markdownLinkRE = regexp.MustCompile(`(^|[^!])\[([^\]\n]+)\]\((<[^>\n]+>|[^)\s]+)(?:\s+["'][^)]*["'])?\)`)

func selectBookmarks(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	items, err := bookmarkItems(cfg.Bookmarks, cfg.Editor, bookmarksProjectName(rt))
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	return picker.SelectWithExpect(ctx, "bookmarks> ", items, viewSwitchKeys, viewSwitchFooter())
}

func bookmarkItems(cfg config.BookmarksConfig, editor config.EditorConfig, projectName string) ([]picker.Item[menuItem], error) {
	links, err := collectBookmarks(cfg, projectName)
	if err != nil {
		return nil, err
	}
	items := make([]picker.Item[menuItem], 0, len(links))
	for _, link := range links {
		items = append(items, picker.Item[menuItem]{
			Label: bookmarkLabel(link),
			Value: menuItem{dispatch: bookmarkDispatch(link, editor, cfg.Open)},
		})
	}
	return items, nil
}

func collectBookmarks(cfg config.BookmarksConfig, projectName string) ([]bookmarkItem, error) {
	links := make([]bookmarkItem, 0)
	for _, source := range bookmarkSources(cfg.Dirs, projectName) {
		files, err := bookmarkMarkdownFiles(source.Path, cfg.IgnorePatterns)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			found, err := extractBookmarksFromFile(file, source.Name)
			if err != nil {
				return nil, err
			}
			links = append(links, found...)
		}
	}
	return links, nil
}

type bookmarkSource struct {
	Name string
	Path string
}

func bookmarkSources(dirs []string, projectName string) []bookmarkSource {
	projectName = strings.TrimSpace(projectName)
	sources := make([]bookmarkSource, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if strings.Contains(dir, "{project}") {
			if projectName == "" {
				continue
			}
			dir = strings.ReplaceAll(dir, "{project}", projectName)
		}
		path := filepath.Clean(expandPath(dir))
		sources = append(sources, bookmarkSource{
			Name: bookmarkSourceName(path),
			Path: path,
		})
	}
	return sources
}

func bookmarkSourceName(path string) string {
	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == string(filepath.Separator) {
		return shortenHome(path)
	}
	return name
}

func bookmarkMarkdownFiles(root string, ignorePatterns []string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel != "." && bookmarkPathIgnored(rel, ignorePatterns) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if bookmarkPathIgnored(rel, ignorePatterns) {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func bookmarkPathIgnored(rel string, patterns []string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return false
	}
	parts := strings.Split(rel, "/")
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.Contains(pattern, "/") && strings.Contains(rel, filepath.ToSlash(pattern)) {
			return true
		}
		for _, part := range parts {
			if strings.Contains(part, pattern) {
				return true
			}
		}
	}
	return false
}

func extractBookmarksFromFile(path string, sourceName string) ([]bookmarkItem, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	items := make([]bookmarkItem, 0)
	for i, line := range strings.Split(string(b), "\n") {
		for _, match := range markdownLinkRE.FindAllStringSubmatch(line, -1) {
			if len(match) < 4 {
				continue
			}
			text := strings.TrimSpace(match[2])
			target := strings.Trim(strings.TrimSpace(match[3]), "<>")
			if text == "" || target == "" || strings.HasPrefix(target, "#") {
				continue
			}
			items = append(items, bookmarkItem{
				Text:       text,
				Target:     target,
				SourceFile: path,
				SourceName: sourceName,
				Line:       i + 1,
			})
		}
	}
	return items, nil
}

func bookmarkDispatch(item bookmarkItem, editor config.EditorConfig, openConfig config.OpenConfig) action.Dispatch {
	if isHTTPBookmark(item.Target) {
		return action.Dispatch{
			Mode: "shell",
			Cmd:  "open " + shellquote.Quote(item.Target),
		}
	}
	target := resolveBookmarkFile(item.Target, item.SourceFile)
	return fileOpenDispatch(linkscan.Item{Kind: linkscan.KindFile, Target: target}, filepath.Dir(target), editor, openConfig)
}

func bookmarkLabel(item bookmarkItem) string {
	source := fmt.Sprintf("%s:%d", shortenHome(item.SourceFile), item.Line)
	sourceName := item.SourceName
	if sourceName == "" {
		sourceName = "bookmark"
	}
	return fmt.Sprintf("%s  %s  %s  %s",
		colorKind(sourceName),
		colorPaneTitle(item.Text),
		dim(bookmarkTargetLabel(item.Target)),
		dim(source),
	)
}

func bookmarkTargetLabel(target string) string {
	if isHTTPBookmark(target) {
		return target
	}
	return shortenHome(target)
}

func isHTTPBookmark(target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

func resolveBookmarkFile(target string, sourceFile string) string {
	path := strings.TrimSpace(target)
	if u, err := url.Parse(path); err == nil && u.Scheme == "file" {
		path = u.Path
	}
	path, _, _ = strings.Cut(path, "#")
	path, _, _ = strings.Cut(path, "?")
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	path = expandPath(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), path))
}

func bookmarksProjectName(rt runtimeContext) string {
	for _, path := range []string{rt.SessionPath, rt.OriginPath} {
		if name := projectNameFromPath(path); name != "" {
			return name
		}
	}
	return rt.SessionName
}

func projectNameFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		projectsRoot := filepath.Join(home, "projects")
		if rel, err := filepath.Rel(projectsRoot, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
			return first
		}
	}
	base := filepath.Base(filepath.Clean(path))
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}
