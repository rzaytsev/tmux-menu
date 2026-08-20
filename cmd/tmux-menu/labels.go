package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tmux-menu/internal/config"
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
	if !p.AutomaticRename && p.WindowName != "" && p.WindowName != title {
		title = p.WindowName + " | " + title
	}
	path := shortenHome(p.CurrentPath)
	state := ""
	if p.PaneID == currentPaneID {
		state = "  " + dim("current")
	}
	return fmt.Sprintf("%s     %s  %s  %s  %s%s",
		colorKind("pane"), dim(p.SessionName+"/"+p.WindowIndex+"."+p.PaneIndex), colorPaneTitle(title), colorCommand(p.CurrentCommand), dim(path), state)
}

func agentPaneLabel(p tmux.Pane, currentPaneID string, status agentStatus, name string, sessionColor string) string {
	return agentPaneLabelWithConfig(p, currentPaneID, status, name, sessionColor, config.Default().Agents)
}

func agentPaneLabelWithConfig(p tmux.Pane, currentPaneID string, status agentStatus, name string, sessionColor string, agentsConfig config.AgentsConfig) string {
	title := agentListPaneTitle(p, name)
	if p.PaneID != "" && p.PaneID == currentPaneID {
		title = agentsConfig.Icons.Current + title
	}
	return fmt.Sprintf("%s %s %s  %s  %s",
		colorAgentStatusWithConfig(status, agentsConfig), colorAgentIcon(name, agentsConfig), colorSessionWithColor(shortUUID(p.SessionName), sessionColor), colorAgentThread(title, agentsConfig), colorAgentText(shortenHome(p.CurrentPath), agentsConfig.Colors.Workdir, false))
}

func agentListPaneLabel(p tmux.Pane, currentPaneID string, status agentStatus, name string, sessionColor string) string {
	return agentListPaneLabelWithConfig(p, currentPaneID, status, name, sessionColor, config.Default().Agents)
}

func agentListPaneLabelWithConfig(p tmux.Pane, currentPaneID string, status agentStatus, name string, sessionColor string, agentsConfig config.AgentsConfig) string {
	title := agentListPaneTitle(p, name)
	if p.PaneID != "" && p.PaneID == currentPaneID {
		title = agentsConfig.Icons.Current + title
	}
	return fmt.Sprintf("%s %s %s %s  %s",
		colorSessionWithColor(shortUUID(p.SessionName), sessionColor), colorAgentStatusWithConfig(status, agentsConfig), colorAgentIcon(name, agentsConfig), colorAgentThread(title, agentsConfig), colorAgentText(agentListWorkdir(p.CurrentPath), agentsConfig.Colors.Workdir, false))
}

func agentListWorkdir(path string) string {
	return strings.TrimPrefix(shortenHome(path), "~/projects/")
}

func agentListPaneTitle(p tmux.Pane, name string) string {
	title := agentPaneTitle(p)
	if name != "claude" {
		return title
	}
	fields := strings.Fields(title)
	if len(fields) == 0 || (!claudeWorkingTitleMarker(fields[0]) && !claudeWaitingTitleMarker(fields[0])) {
		return title
	}
	if len(fields) > 1 {
		return strings.TrimSpace(strings.TrimPrefix(title, fields[0]))
	}
	return filepath.Base(p.CurrentPath)
}

func shortUUID(name string) string {
	parts := strings.Split(name, "-")
	lengths := []int{8, 4, 4, 4, 12}
	if len(parts) != len(lengths) {
		return name
	}
	for i, part := range parts {
		if len(part) != lengths[i] || !isHex(part) {
			return name
		}
	}
	return parts[0]
}

func isHex(value string) bool {
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return value != ""
}

func agentPaneTitle(p tmux.Pane) string {
	if info, ok := codexPaneTitleInfoFromPane(p); ok {
		switch {
		case info.threadTitle != "":
			return shortUUID(info.threadTitle)
		case info.currentDir != "":
			return shortUUID(info.currentDir)
		case p.CurrentPath != "":
			return filepath.Base(p.CurrentPath)
		}
	}
	title := cleanPaneTitle(p.PaneTitle)
	if title == "" {
		title = p.CurrentCommand
	}
	return shortUUID(title)
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
	ansiReset         = "\x1b[0m"
	ansiBold          = "\x1b[1m"
	ansiDim           = "\x1b[2m"
	ansiDefault       = "\x1b[39m"
	ansiBlack         = "\x1b[30m"
	ansiRed           = "\x1b[31m"
	ansiGreen         = "\x1b[32m"
	ansiYellow        = "\x1b[33m"
	ansiBlue          = "\x1b[34m"
	ansiMagenta       = "\x1b[35m"
	ansiCyan          = "\x1b[36m"
	ansiWhite         = "\x1b[37m"
	ansiBrightBlack   = "\x1b[90m"
	ansiBrightRed     = "\x1b[91m"
	ansiBrightGreen   = "\x1b[92m"
	ansiBrightYellow  = "\x1b[93m"
	ansiBrightBlue    = "\x1b[94m"
	ansiBrightMagenta = "\x1b[95m"
	ansiBrightCyan    = "\x1b[96m"
	ansiBrightWhite   = "\x1b[97m"
	ansiOrange        = "\x1b[38;5;208m"
)

func colorKind(kind string) string {
	return ansiBlue + kind + ansiReset
}

func colorSession(name string) string {
	return ansiBold + ansiCyan + name + ansiReset
}

func colorSessionWithColor(name string, color string) string {
	return ansiBold + ansiColor(color) + name + ansiReset
}

func ansiColor(color string) string {
	switch color {
	case "default":
		return ansiDefault
	case "black":
		return ansiBlack
	case "red":
		return ansiRed
	case "green":
		return ansiGreen
	case "yellow":
		return ansiYellow
	case "blue":
		return ansiBlue
	case "magenta":
		return ansiMagenta
	case "cyan":
		return ansiCyan
	case "white":
		return ansiWhite
	case "bright_black":
		return ansiBrightBlack
	case "bright_red":
		return ansiBrightRed
	case "bright_green":
		return ansiBrightGreen
	case "bright_yellow":
		return ansiBrightYellow
	case "bright_blue":
		return ansiBrightBlue
	case "bright_magenta":
		return ansiBrightMagenta
	case "bright_cyan":
		return ansiBrightCyan
	case "bright_white":
		return ansiBrightWhite
	case "orange":
		return ansiOrange
	case "dim":
		return ansiDim
	default:
		return ansiCyan
	}
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
	return colorAgentStatusWithConfig(status, config.Default().Agents)
}

func colorAgentStatusWithConfig(status agentStatus, agentsConfig config.AgentsConfig) string {
	switch status {
	case agentStatusAttention:
		return colorAgentText(agentsConfig.Icons.Attention, agentsConfig.Colors.Attention, true)
	case agentStatusWorking:
		return colorAgentText(agentsConfig.Icons.Working, agentsConfig.Colors.Working, false)
	case agentStatusCompleted:
		return colorAgentText(agentsConfig.Icons.Completed, agentsConfig.Colors.Completed, false)
	case agentStatusWaiting:
		return colorAgentText(agentsConfig.Icons.Waiting, agentsConfig.Colors.Waiting, false)
	default:
		return colorAgentText(agentsConfig.Icons.Unknown, agentsConfig.Colors.Unknown, false)
	}
}

func colorAgentIcon(name string, agentsConfig config.AgentsConfig) string {
	switch name {
	case "codex":
		return colorAgentText(agentsConfig.Icons.Codex, agentsConfig.Colors.Codex, false)
	case "claude":
		return colorAgentText(agentsConfig.Icons.Claude, agentsConfig.Colors.Claude, false)
	default:
		icon := agentsConfig.Icons.Other
		if icon == "" {
			icon = name
		}
		return colorAgentText(icon, agentsConfig.Colors.Other, false)
	}
}

func colorAgentThread(title string, agentsConfig config.AgentsConfig) string {
	return colorAgentText(title, agentsConfig.Colors.Thread, true)
}

func colorAgentText(text string, color string, bold bool) string {
	style := ansiColor(color)
	if bold {
		style = ansiBold + style
	}
	return style + text + ansiReset
}

func commandClass(command string) string {
	command = strings.ToLower(command)
	switch {
	case knownAgentName(command) != "":
		return "agent"
	case strings.Contains(command, "mosh"),
		strings.Contains(command, "ssh"):
		return "remote"
	default:
		return ""
	}
}

func knownAgentName(command string) string {
	command = strings.ToLower(command)
	switch {
	case strings.Contains(command, "codex"):
		return "codex"
	case strings.Contains(command, "claude"):
		return "claude"
	case strings.Contains(command, "opencode"):
		return "opencode"
	case strings.Contains(command, "cursor-agent"):
		return "cursor-agent"
	case strings.Contains(command, "aider"):
		return "aider"
	case strings.Contains(command, "gemini"):
		return "gemini"
	default:
		return ""
	}
}

func processCommandClass(command string) string {
	if processAgentName(command) != "" {
		return "agent"
	}
	return ""
}

func processAgentName(command string) string {
	if isAgentHookCommand(command) {
		return ""
	}
	for _, field := range strings.Fields(command) {
		name := strings.Trim(field, `"'`)
		if name == "" {
			continue
		}
		if agentName := knownAgentName(filepath.Base(name)); agentName != "" {
			return agentName
		}
	}
	return ""
}

func isAgentHookCommand(command string) bool {
	fields := strings.Fields(command)
	for i := 0; i+2 < len(fields); i++ {
		first := strings.Trim(fields[i], `"'`)
		second := strings.Trim(fields[i+1], `"'`)
		third := strings.Trim(fields[i+2], `"'`)
		if first == "agent-hook" && (second == "ingest" || second == "trace") &&
			(third == "codex" || third == "claude") {
			return true
		}
	}
	return false
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
