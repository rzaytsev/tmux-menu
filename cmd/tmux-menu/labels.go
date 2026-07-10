package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tmux-menu/internal/tmux"
)

func sessionLabel(p tmux.Pane, panes []tmux.Pane, currentSessionID string) string {
	state := ""
	if p.SessionID == currentSessionID {
		state = "  " + dim("current")
	}
	return fmt.Sprintf("%s  %s  %s%s",
		colorKind("session"), colorSession(p.SessionName), dim(fmt.Sprintf("(%d panes)", sessionPaneCount(p.SessionID, panes))), state)
}

func sessionPaneCount(sessionID string, panes []tmux.Pane) int {
	count := 0
	for _, p := range panes {
		if p.SessionID == sessionID {
			count++
		}
	}
	return count
}

func paneLabel(p tmux.Pane, currentPaneID string) string {
	title := cleanPaneTitle(p.PaneTitle)
	if title == "" {
		title = p.CurrentCommand
	}
	path := shortenHome(p.CurrentPath)
	state := ""
	if p.PaneID == currentPaneID {
		state = "  " + dim("current")
	}
	return fmt.Sprintf("%s     %s  %s  %s  %s%s",
		colorKind("pane"), dim(p.SessionName+"/"+p.WindowIndex+"."+p.PaneIndex), colorPaneTitle(title), colorCommand(p.CurrentCommand), dim(path), state)
}

func agentPaneLabel(p tmux.Pane, currentPaneID string, status agentStatus) string {
	title := agentPaneTitle(p)
	state := ""
	if p.PaneID == currentPaneID {
		state = "  " + dim("current")
	}
	return fmt.Sprintf("%s    %s  %s  %s  %s  %s%s",
		colorKind("agent"), dim(p.SessionName+"/"+p.WindowIndex+"."+p.PaneIndex), colorPaneTitle(title), colorCommand(p.CurrentCommand), colorAgentStatus(status), dim(shortenHome(p.CurrentPath)), state)
}

func agentPaneTitle(p tmux.Pane) string {
	if info, ok := codexPaneTitleInfoFromPane(p); ok {
		switch {
		case info.threadTitle != "":
			return info.threadTitle
		case info.currentDir != "":
			return info.currentDir
		case p.CurrentPath != "":
			return filepath.Base(p.CurrentPath)
		}
	}
	title := cleanPaneTitle(p.PaneTitle)
	if title == "" {
		title = p.CurrentCommand
	}
	return title
}

func cleanPaneTitle(title string) string {
	title = strings.TrimSpace(title)
	user := os.Getenv("USER")
	host, _ := os.Hostname()
	if user != "" && host != "" {
		for _, candidate := range localHostCandidates(host) {
			localPrefix := user + "@" + candidate
			if strings.HasPrefix(title, localPrefix+":") {
				return strings.TrimPrefix(title, localPrefix+":")
			}
			if title == localPrefix {
				return ""
			}
		}
	}

	if user != "" && strings.HasPrefix(title, user+"@") {
		_, rest, ok := strings.Cut(title, ":")
		if ok && looksLocalPromptRest(rest) {
			return rest
		}
		if !ok {
			title = ""
		}
	}
	return title
}

func localHostCandidates(host string) []string {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	candidates := []string{host}
	if short, _, ok := strings.Cut(host, "."); ok && short != "" {
		candidates = append(candidates, short)
	}
	if !strings.HasSuffix(host, ".local") {
		candidates = append(candidates, host+".local")
	}
	return candidates
}

func looksLocalPromptRest(rest string) bool {
	return strings.HasPrefix(rest, "~") ||
		strings.HasPrefix(rest, "/") ||
		strings.HasPrefix(rest, "$") ||
		rest == ""
}

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiBlue    = "\x1b[34m"
	ansiCyan    = "\x1b[36m"
	ansiGreen   = "\x1b[32m"
	ansiMagenta = "\x1b[35m"
	ansiRed     = "\x1b[31m"
	ansiYellow  = "\x1b[33m"
)

func colorKind(kind string) string {
	return ansiBlue + kind + ansiReset
}

func colorSession(name string) string {
	return ansiBold + ansiCyan + name + ansiReset
}

func colorPaneTitle(title string) string {
	return ansiBold + title + ansiReset
}

func colorCommand(command string) string {
	switch commandClass(command) {
	case "agent":
		return ansiMagenta + command + ansiReset
	case "remote":
		return ansiYellow + command + ansiReset
	default:
		return ansiGreen + command + ansiReset
	}
}

func colorAgentStatus(status agentStatus) string {
	switch status {
	case agentStatusAttention:
		return ansiBold + ansiRed + "attention" + ansiReset
	case agentStatusWorking:
		return ansiGreen + "working" + ansiReset
	case agentStatusWaiting:
		return ansiYellow + "waiting" + ansiReset
	default:
		return ansiDim + "unknown" + ansiReset
	}
}

func commandClass(command string) string {
	command = strings.ToLower(command)
	switch {
	case strings.Contains(command, "codex"),
		strings.Contains(command, "claude"),
		strings.Contains(command, "opencode"),
		strings.Contains(command, "aider"),
		strings.Contains(command, "cursor-agent"),
		strings.Contains(command, "gemini"):
		return "agent"
	case strings.Contains(command, "mosh"),
		strings.Contains(command, "ssh"):
		return "remote"
	default:
		return ""
	}
}

func processCommandClass(command string) string {
	for _, field := range strings.Fields(command) {
		name := strings.Trim(field, `"'`)
		if name == "" {
			continue
		}
		if commandClass(filepath.Base(name)) == "agent" {
			return "agent"
		}
	}
	return ""
}
func dim(s string) string {
	if s == "" {
		return ""
	}
	return ansiDim + s + ansiReset
}
func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func expandPath(path string) string {
	path = os.ExpandEnv(path)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + strings.TrimPrefix(path, "~")
		}
	}
	return path
}
