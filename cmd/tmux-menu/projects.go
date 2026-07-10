package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tmux-menu/internal/action"
	"tmux-menu/internal/picker"
)

func selectProjects(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, _, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	projects, err := listProjects(cfg.Projects.Roots)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	return picker.SelectWithExpect(ctx, "projects> ", projectItems(projects, cfg.Projects.BootstrapFile), viewSwitchKeys, viewSwitchHeaderForConfig(cfg))
}
func listProjects(roots []string) ([]string, error) {
	return listProjectsInRoots(roots)
}

func listProjectsInRoots(roots []string) ([]string, error) {
	if len(roots) == 0 {
		roots = []string{"~/projects"}
	}
	seen := make(map[string]bool)
	projects := make([]string, 0)
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		root = expandPath(root)
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name())
			if seen[path] {
				continue
			}
			seen[path] = true
			projects = append(projects, path)
		}
	}
	sort.Strings(projects)
	return projects, nil
}

func projectItems(projects []string, bootstrapFile string) []picker.Item[menuItem] {
	sessionNames := projectSessionNames(projects)
	items := make([]picker.Item[menuItem], 0, len(projects))
	for _, path := range projects {
		sessionName := sessionNames[path]
		items = append(items, picker.Item[menuItem]{
			Label: projectLabelWithSession(path, bootstrapFile, sessionName),
			Value: menuItem{dispatch: projectDispatchWithSession(path, bootstrapFile, sessionName)},
		})
	}
	return items
}

func projectSessionNames(projects []string) map[string]string {
	counts := make(map[string]int)
	for _, path := range projects {
		counts[action.ProjectSessionName(path)]++
	}
	names := make(map[string]string, len(projects))
	for _, path := range projects {
		name := action.ProjectSessionName(path)
		if counts[name] > 1 {
			name = action.UniqueProjectSessionName(path)
		}
		names[path] = name
	}
	return names
}

func projectLabel(path string, bootstrapFile string) string {
	return projectLabelWithSession(path, bootstrapFile, action.ProjectSessionName(path))
}

func projectLabelWithSession(path string, bootstrapFile string, sessionName string) string {
	sessionNote := ""
	if sessionName != "" && sessionName != action.ProjectSessionName(path) {
		sessionNote = "  " + dim("session "+sessionName)
	}
	return fmt.Sprintf("%s  %s  %s  %s%s", colorKind("project"), colorSession(filepath.Base(path)), dim(shortenHome(path)), dim(projectBootstrapLabel(path, bootstrapFile)), sessionNote)
}

func projectDispatch(path string, bootstrapFile string) action.Dispatch {
	return action.Project(path, bootstrapFile)
}

func projectDispatchWithSession(path string, bootstrapFile string, sessionName string) action.Dispatch {
	return action.ProjectWithSessionName(path, bootstrapFile, sessionName)
}

func projectBootstrapLabel(path string, bootstrapFile string) string {
	bootstrapFile = strings.TrimSpace(bootstrapFile)
	if bootstrapFile == "" {
		bootstrapFile = ".tmux-sessionizer"
	}
	if _, err := os.Stat(filepath.Join(path, bootstrapFile)); err == nil {
		return "bootstrap"
	}
	return "no bootstrap"
}
