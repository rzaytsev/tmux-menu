package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tmux-menu/internal/config"
	linkscan "tmux-menu/internal/links"
	"tmux-menu/internal/picker"
)

func selectStatus(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	if len(cfg.Status.Targets) > 0 {
		return selectStatusRadar(ctx, cfg, rt)
	}
	if strings.TrimSpace(cfg.Status.Command) != "" {
		return picker.Result[menuItem]{}, runStatusCommand(ctx, cfg.Status.Command, rt)
	}
	sessionRoot := rt.SessionPath
	if sessionRoot == "" {
		sessionRoot = rt.OriginPath
	}
	items, err := statusItems(cfg.Status, cfg.Editor, sessionRoot)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	return picker.SelectWithExpectAndPreviewOptions(ctx, "status> ", items, viewSwitchKeys, statusFooter(cfg), cfg.Status.PreviewCommand, picker.Options{
		PreviewWindow: pickerPreviewWindow(cfg.Picker.PreviewWidth, "hidden", "wrap"),
		Bindings:      []string{"space:toggle-preview"},
	})
}

func statusFooter(cfg config.Config) string {
	statuses := make([]string, 0, len(cfg.Status.Statuses))
	for _, status := range cfg.Status.Statuses {
		status = strings.TrimSpace(status)
		if status == "" {
			continue
		}
		statuses = append(statuses, strings.ToUpper(status))
	}
	footer := strings.Join(statuses, " / ")
	if footer != "" {
		footer += " | "
	}
	footer += "Space preview | Enter edit | Ctrl-C cancel"
	footer += "\n" + viewSwitchFooter()
	return footer
}

func statusItems(cfg config.StatusConfig, editor config.EditorConfig, sessionRoot string) ([]picker.Item[menuItem], error) {
	files := make([]statusFile, 0)
	for _, statusDir := range cfg.StatusDirs {
		resolved := resolveStatusDir(statusDir, sessionRoot)
		found, err := listStatusFiles(resolved, cfg.Statuses, cfg.IgnorePatterns)
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
	}
	items := make([]picker.Item[menuItem], 0, len(files))
	for _, file := range files {
		items = append(items, picker.Item[menuItem]{
			Label:   statusLabel(file),
			Preview: file.Path,
			Value:   menuItem{dispatch: fileOpenDispatch(linkscan.Item{Kind: linkscan.KindFile, Target: file.Path}, sessionRoot, editor, cfg.Open)},
		})
	}
	return items, nil
}

type statusFile struct {
	Path    string
	Status  string
	Title   string
	Summary string
}

func listStatusFiles(root string, statuses []string, ignorePatterns []string) ([]statusFile, error) {
	root = filepath.Clean(root)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	files := make([]statusFile, 0)
	for _, status := range statuses {
		status = strings.TrimSpace(status)
		if status == "" {
			continue
		}
		statusRoot := filepath.Join(root, status)
		info, err := os.Stat(statusRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		found, err := listStatusLaneFiles(root, statusRoot, status, ignorePatterns)
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
	}
	return files, nil
}

func listStatusLaneFiles(root string, statusRoot string, status string, ignorePatterns []string) ([]statusFile, error) {
	files := make([]statusFile, 0)
	err := filepath.WalkDir(statusRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if statusFileIgnored(rel, ignorePatterns) {
			return nil
		}
		title, summary := statusTaskMeta(path)
		files = append(files, statusFile{
			Path:    path,
			Status:  status,
			Title:   title,
			Summary: summary,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func statusFileIgnored(rel string, patterns []string) bool {
	base := filepath.Base(rel)
	rel = filepath.ToSlash(rel)
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		target := base
		if strings.Contains(pattern, "/") {
			target = rel
		}
		if ok, err := filepath.Match(pattern, target); err == nil && ok {
			return true
		}
	}
	return false
}

func resolveStatusDir(path string, sessionRoot string) string {
	path = expandPath(strings.TrimSpace(path))
	if path == "" {
		path = "./todo"
	}
	if filepath.IsAbs(path) || sessionRoot == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(expandPath(sessionRoot), path))
}

func statusLabel(file statusFile) string {
	return fmt.Sprintf("%s | %s | %s",
		colorStatusBadge(file.Status),
		colorPaneTitle(truncateStatusText(file.Title, 38)),
		truncateStatusText(file.Summary, 72),
	)
}

func statusTaskMeta(path string) (string, string) {
	title := statusTitleFromPath(path)
	summary := ""
	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if summary == "" {
				if before, after, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(before), "summary") {
					summary = strings.TrimSpace(after)
				}
			}
			if summary != "" {
				break
			}
		}
	}
	if summary == "" {
		summary = "(no summary)"
	}
	return cleanStatusText(title), cleanStatusText(summary)
}

func statusTitleFromPath(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	return cleanStatusText(name)
}

func cleanStatusText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func truncateStatusText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
}

func colorStatusBadge(status string) string {
	badge := fmt.Sprintf("%-7s", strings.ToUpper(status))
	switch strings.ToLower(status) {
	case "done":
		return ansiGreen + badge + ansiReset
	case "doing":
		return ansiYellow + badge + ansiReset
	case "new":
		return ansiCyan + badge + ansiReset
	case "backlog":
		return ansiMagenta + badge + ansiReset
	case "old", "archive", "archived":
		return ansiDim + badge + ansiReset
	default:
		return ansiBlue + badge + ansiReset
	}
}
