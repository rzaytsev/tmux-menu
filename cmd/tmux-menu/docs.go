package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"tmux-menu/internal/config"
	"tmux-menu/internal/fileutil"
	linkscan "tmux-menu/internal/links"
	"tmux-menu/internal/picker"
)

func selectDocs(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	items, err := docItems(ctx, cfg.Docs, cfg.Editor, rt)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	return picker.SelectWithExpectAndPreviewOptions(ctx, "docs> ", items, filePickerKeys(), "Enter edit  Ctrl-Y copy path\nSpace preview\n"+viewSwitchFooter(), cfg.Docs.PreviewCommand, picker.Options{
		PreviewWindow: pickerPreviewWindow(cfg.Picker.PreviewWidth, "hidden", "wrap"),
		Bindings:      []string{"space:toggle-preview"},
	})
}

func docItems(ctx context.Context, cfg config.DocsConfig, editor config.EditorConfig, rt runtimeContext) ([]picker.Item[menuItem], error) {
	root := rt.SessionPath
	if root == "" {
		root = rt.OriginPath
	}
	seen := make(map[string]bool)
	var items []picker.Item[menuItem]
	for _, source := range bookmarkSources(cfg.Dirs, bookmarksProjectName(rt)) {
		path := source.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		files, err := bookmarkMarkdownFilesContext(ctx, path, cfg.IgnorePatterns)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if seen[file] {
				continue
			}
			seen[file] = true
			items = append(items, picker.Item[menuItem]{
				Label:   fmt.Sprintf("%s  %s  %s", colorKind("doc"), colorPaneTitle(docTitle(file)), dim(shortenHome(file))),
				Preview: file,
				Value: menuItem{
					dispatch:   fileOpenDispatch(linkscan.Item{Kind: linkscan.KindFile, Target: file}, root, editor, cfg.Open),
					copyTarget: file,
				},
			})
		}
	}
	return items, nil
}

func docTitle(path string) string {
	f, err := fileutil.OpenRegular(path)
	if err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(io.LimitReader(f, 64<<10))
		for scanner.Scan() {
			if title, ok := strings.CutPrefix(scanner.Text(), "# "); ok && strings.TrimSpace(title) != "" {
				return strings.TrimSpace(title)
			}
		}
	}
	return statusTitleFromPath(path)
}

func filePickerKeys() []string {
	return append(append([]string(nil), viewSwitchKeys...), "ctrl-y")
}
