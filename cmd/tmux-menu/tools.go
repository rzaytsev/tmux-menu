package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/shellquote"
)

func selectTools(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	items := toolsItems(cfg, rt.OriginPath, rt.SessionName, rt.SessionPath)
	return picker.SelectWithExpect(ctx, "tools> ", items, viewSwitchKeys, viewSwitchHeaderForConfig(cfg))
}

func toolsItems(cfg config.Config, originPath, sessionName, sessionPath string) []picker.Item[menuItem] {
	quickDirBase := sessionPath
	if quickDirBase == "" {
		quickDirBase = originPath
	}
	items := make([]picker.Item[menuItem], 0, len(cfg.Commands)+len(cfg.QuickDirs))
	for _, c := range cfg.Commands {
		if !commandMatchesSession(c, sessionName) {
			continue
		}
		items = append(items, picker.Item[menuItem]{
			Label: commandLabel(c),
			Value: menuItem{dispatch: action.FromCommand(c, cfg.Popup, originPath)},
		})
	}
	for _, target := range makefileTargetsForDirs(sessionPath, originPath) {
		items = append(items, picker.Item[menuItem]{
			Label: makeTargetLabel(target),
			Value: menuItem{dispatch: makeTargetDispatch(target)},
		})
	}
	for _, d := range cfg.QuickDirs {
		if !quickDirMatchesSession(d, sessionName) {
			continue
		}
		items = append(items, picker.Item[menuItem]{
			Label: quickDirLabel(d, quickDirBase),
			Value: menuItem{dispatch: quickDirDispatch(d, quickDirBase)},
		})
	}
	return items
}

func toolsItemsForTest() []picker.Item[menuItem] {
	cfg := config.Default()
	cfg.Commands = []config.Command{{Title: "Git status", Category: "Git", Mode: "paste", Cmd: "git status"}}
	cfg.QuickDirs = []config.QuickDir{{Title: "notes", Path: "~/notes"}}
	return toolsItems(cfg, "", "", "")
}
func commandLabel(c config.Command) string {
	category := c.Category
	if category == "" {
		category = "Commands"
	}
	desc := c.Description
	if desc != "" {
		desc = "  " + desc
	}
	return fmt.Sprintf("%s      %s  %s  %s%s", colorKind("cmd"), dim(category), c.Title, dim("["+c.Mode+"]"), dim(desc))
}

func quickDirLabel(d config.QuickDir, base string) string {
	command := strings.TrimSpace(d.Command)
	if command != "" {
		command = "  " + dim("["+command+"]")
	}
	return fmt.Sprintf("%s      %s  %s%s", colorKind("dir"), d.Title, dim(shortenHome(resolveQuickDirPath(d.Path, base))), command)
}

func quickDirDispatch(d config.QuickDir, base string) action.Dispatch {
	path := resolveQuickDirPath(d.Path, base)
	cmd := "cd " + shellquote.Quote(path)
	if command := strings.TrimSpace(d.Command); command != "" {
		cmd += " && " + command
	}
	return action.Dispatch{
		Mode:  "paste",
		Cmd:   cmd,
		Enter: true,
	}
}

func quickDirMatchesSession(d config.QuickDir, sessionName string) bool {
	return d.Session == "" || d.Session == sessionName
}

func commandMatchesSession(c config.Command, sessionName string) bool {
	return c.Session == "" || c.Session == sessionName
}

type makefileTarget struct {
	Dir    string
	Target string
}

func makefileTargetsForDirs(dirs ...string) []makefileTarget {
	seenDirs := make(map[string]bool)
	targets := make([]makefileTarget, 0)
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		dir = filepath.Clean(expandPath(dir))
		if seenDirs[dir] {
			continue
		}
		seenDirs[dir] = true
		targets = append(targets, makefileTargetsInDir(dir)...)
	}
	return targets
}

func makefileTargetsInDir(dir string) []makefileTarget {
	b, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	targets := make([]makefileTarget, 0)
	for _, line := range strings.Split(string(b), "\n") {
		names, ok := parseMakeTargetLine(line)
		if !ok {
			continue
		}
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			targets = append(targets, makefileTarget{Dir: dir, Target: name})
		}
	}
	return targets
}

func parseMakeTargetLine(line string) ([]string, bool) {
	if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
		return nil, false
	}
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ".") {
		return nil, false
	}
	if isMakeVariableAssignment(line) {
		return nil, false
	}
	left, _, ok := strings.Cut(line, ":")
	if !ok || strings.Contains(left, "=") {
		return nil, false
	}
	names := make([]string, 0)
	for _, field := range strings.Fields(left) {
		if validMakeTarget(field) {
			names = append(names, field)
		}
	}
	return names, len(names) > 0
}

func isMakeVariableAssignment(line string) bool {
	colon := strings.Index(line, ":")
	for _, op := range []string{":::=", "::=", ":=", "+=", "?=", "!=", "="} {
		idx := strings.Index(line, op)
		if idx == -1 {
			continue
		}
		if colon == -1 || idx <= colon {
			return true
		}
	}
	return false
}

func validMakeTarget(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.Contains(name, "%") {
		return false
	}
	for _, r := range name {
		if ('a' <= r && r <= 'z') ||
			('A' <= r && r <= 'Z') ||
			('0' <= r && r <= '9') ||
			r == '_' || r == '-' || r == '.' || r == '/' {
			continue
		}
		return false
	}
	return true
}

func makeTargetLabel(target makefileTarget) string {
	return fmt.Sprintf("%s     %s  %s", colorKind("make"), target.Target, dim(shortenHome(target.Dir)))
}

func makeTargetDispatch(target makefileTarget) action.Dispatch {
	return action.Dispatch{
		Mode:  "paste",
		Cmd:   "make -C " + makeDirArg(target.Dir) + " " + target.Target,
		Enter: true,
	}
}

func makeDirArg(path string) string {
	path = filepath.Clean(path)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return shellquote.Quote(path)
	}
	home = filepath.Clean(home)
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return shellquote.Quote(path)
	}
	if rel == "." {
		return `"$HOME"`
	}
	return `"$HOME/` + shellDoubleQuoteContent(filepath.ToSlash(rel)) + `"`
}

func shellDoubleQuoteContent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '"', '$', '`':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func resolveQuickDirPath(path string, base string) string {
	path = expandPath(path)
	if filepath.IsAbs(path) || base == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(expandPath(base), path))
}
