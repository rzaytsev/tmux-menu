package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tmux-menu/internal/config"
	"tmux-menu/internal/fileutil"
	linkscan "tmux-menu/internal/links"
	"tmux-menu/internal/picker"
)

type bookmarkItem struct {
	Text       string
	Target     string
	SourceFile string
	SourceName string
	Line       int
}

func selectBookmarks(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	if cfg.Bookmarks.Dirs == nil {
		root := rt.SessionPath
		if root == "" {
			home, _ := os.UserHomeDir()
			root = filepath.Join(home, "notes")
		}
		cfg.Bookmarks.Dirs = []string{root}
	}
	links, err := collectBookmarks(cfg.Bookmarks, bookmarksProjectName(rt))
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	var targets []linkscan.Item
	for _, link := range links {
		raw := link.Target
		if strings.HasPrefix(raw, "#") {
			raw = filepath.Base(link.SourceFile) + raw
		}
		item, err := linkscan.Resolve(raw, filepath.Dir(link.SourceFile), true)
		if err != nil {
			continue
		}
		item.Title = link.Text
		targets = append(targets, item)
	}
	return selectTypedLinks(ctx, "bookmarks", linkscan.Deduplicate(targets), cfg, rt)
}

func collectBookmarks(cfg config.BookmarksConfig, projectName string) ([]bookmarkItem, error) {
	links := make([]bookmarkItem, 0)
	seen := make(map[string]bool)
	visited := make(map[string]bool)
	for _, source := range bookmarkSources(cfg.Dirs, projectName) {
		if !filepath.IsAbs(source.Path) {
			source.Path = filepath.Join(cfg.DirsBase, source.Path)
		}
		canonical, err := filepath.EvalSymlinks(source.Path)
		if err != nil {
			return nil, fmt.Errorf("bookmarks directory %s: %w", source.Path, err)
		}
		source.Path = canonical
		info, err := os.Stat(source.Path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("bookmarks directory is not a directory: %s", source.Path)
		}
		files, err := bookmarkMarkdownFilesVisited(context.Background(), source.Path, cfg.IgnorePatterns, visited)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if seen[file] {
				continue
			}
			seen[file] = true
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
	return bookmarkMarkdownFilesContext(context.Background(), root, ignorePatterns)
}

func bookmarkMarkdownFilesContext(ctx context.Context, root string, ignorePatterns []string) ([]string, error) {
	return bookmarkMarkdownFilesVisited(ctx, root, ignorePatterns, nil)
}
func bookmarkMarkdownFilesVisited(ctx context.Context, root string, ignorePatterns []string, visited map[string]bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if visited != nil {
				if visited[path] {
					return filepath.SkipDir
				}
				visited[path] = true
			}
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
			if len(files) >= 10000 {
				return fmt.Errorf("Markdown scan exceeds 10000 files; narrow the configured directories or ignore patterns")
			}
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
	b, err := fileutil.Read(path, 1<<20)
	if errors.Is(err, fileutil.ErrTooLarge) || errors.Is(err, fileutil.ErrNotRegular) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []bookmarkItem
	for _, link := range linkscan.Markdown(b) {
		items = append(items, bookmarkItem{Text: link.Title, Target: link.Target, SourceFile: path, SourceName: sourceName, Line: link.Line})
	}
	return items, nil
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
