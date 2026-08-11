package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/tmux"
)

const (
	statusRadarPreviewCommand = "cat -- {}"
	statusRadarConcurrency    = 4
	statusRadarMaxOutput      = 1 << 20
	statusRadarMaxErrorOutput = 16 << 10
)

type statusRadarOutput struct {
	bytes.Buffer
	Limit     int
	Truncated bool
}

func (output *statusRadarOutput) Write(data []byte) (int, error) {
	size := len(data)
	remaining := output.Limit - output.Len()
	if remaining > 0 {
		if remaining > size {
			remaining = size
		}
		_, _ = output.Buffer.Write(data[:remaining])
	}
	if remaining < size {
		output.Truncated = true
	}
	return size, nil
}

type statusRadarPayload struct {
	Blocks []statusRadarBlock `json:"blocks"`
}

type statusRadarBlock struct {
	Title   string `json:"title"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Details string `json:"details,omitempty"`
}

type statusRadarReport struct {
	Target    config.StatusTarget
	Session   tmux.Pane
	Blocks    []statusRadarBlock
	Status    string
	Summary   string
	Order     int
	Available bool
}

func selectStatusRadar(ctx context.Context, cfg config.Config, rt runtimeContext) (picker.Result[menuItem], error) {
	panes, err := tmux.ListPanes(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	timeout, err := time.ParseDuration(cfg.Status.ReportTimeout)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	reports := collectStatusRadar(ctx, cfg.Status.Targets, panes, rt, timeout)
	previewDir, err := os.MkdirTemp("", "tmux-menu-status-")
	if err != nil {
		return picker.Result[menuItem]{}, fmt.Errorf("create status preview directory: %w", err)
	}
	defer os.RemoveAll(previewDir)

	items, err := statusRadarItems(reports, previewDir)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	return picker.SelectWithExpectAndPreviewOptions(ctx, "status> ", items, viewSwitchKeys, statusRadarFooter(), statusRadarPreviewCommand, picker.Options{
		PreviewWindow: pickerPreviewWindow(cfg.Picker.PreviewWidth, "wrap"),
		Bindings:      []string{"space:toggle-preview"},
	})
}

func collectStatusRadar(ctx context.Context, targets []config.StatusTarget, panes []tmux.Pane, rt runtimeContext, timeout time.Duration) []statusRadarReport {
	sessions := statusRadarSessions(panes)
	reports := make([]statusRadarReport, len(targets))
	semaphore := make(chan struct{}, statusRadarConcurrency)
	var wait sync.WaitGroup

	for i, target := range targets {
		session, ok := sessions[target.Session]
		if !ok {
			reports[i] = failedStatusRadarReport(target, i, "Session is not running")
			continue
		}
		wait.Add(1)
		go func(index int, target config.StatusTarget, session tmux.Pane) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				reports[index] = failedStatusRadarReport(target, index, ctx.Err().Error())
				return
			}

			reportCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			blocks, err := runStatusReporter(reportCtx, target, session, rt)
			if err != nil {
				message := err.Error()
				if errors.Is(reportCtx.Err(), context.DeadlineExceeded) {
					message = "Reporter timed out after " + timeout.String()
				}
				reports[index] = failedStatusRadarReport(target, index, message)
				return
			}
			status, summary := statusRadarRollup(blocks)
			reports[index] = statusRadarReport{
				Target:    target,
				Session:   session,
				Blocks:    blocks,
				Status:    status,
				Summary:   summary,
				Order:     index,
				Available: true,
			}
		}(i, target, session)
	}
	wait.Wait()
	sort.SliceStable(reports, func(i, j int) bool {
		left := statusRadarRank(reports[i].Status)
		right := statusRadarRank(reports[j].Status)
		if left == right {
			return reports[i].Order < reports[j].Order
		}
		return left < right
	})
	return reports
}

func statusRadarSessions(panes []tmux.Pane) map[string]tmux.Pane {
	sessions := make(map[string]tmux.Pane)
	for _, pane := range panes {
		current, ok := sessions[pane.SessionName]
		if !ok || current.SessionPath == "" && pane.SessionPath != "" {
			sessions[pane.SessionName] = pane
		}
	}
	return sessions
}

func runStatusReporter(ctx context.Context, target config.StatusTarget, session tmux.Pane, rt runtimeContext) ([]statusRadarBlock, error) {
	if strings.TrimSpace(session.SessionPath) == "" {
		return nil, errors.New("target session path is unavailable")
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", target.Command)
	cmd.Stdin = strings.NewReader("")
	cmd.Dir = session.SessionPath
	cmd.Env = statusReporterEnv(os.Environ(), map[string]string{
		"TMUX_MENU_ORIGIN_PANE":  rt.OriginPane,
		"TMUX_MENU_ORIGIN_PATH":  rt.OriginPath,
		"TMUX_MENU_SESSION_ID":   session.SessionID,
		"TMUX_MENU_SESSION_NAME": session.SessionName,
		"TMUX_MENU_SESSION_PATH": session.SessionPath,
		"TMUX_MENU_STATUS_TITLE": target.Title,
	})
	stdout := statusRadarOutput{Limit: statusRadarMaxOutput}
	stderr := statusRadarOutput{Limit: statusRadarMaxErrorOutput}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := cleanStatusText(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("reporter failed: %s", truncateStatusText(message, 240))
	}
	if stdout.Truncated {
		return nil, fmt.Errorf("reporter output exceeds %d bytes", statusRadarMaxOutput)
	}

	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var payload statusRadarPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("invalid reporter JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, fmt.Errorf("invalid reporter JSON: %w", err)
	}
	if err := normalizeStatusRadarBlocks(payload.Blocks); err != nil {
		return nil, err
	}
	return payload.Blocks, nil
}

func statusReporterEnv(base []string, values map[string]string) []string {
	out := make([]string, 0, len(base)+len(values))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[name]; replaced {
			continue
		}
		out = append(out, entry)
	}
	for _, name := range []string{
		"TMUX_MENU_ORIGIN_PANE",
		"TMUX_MENU_ORIGIN_PATH",
		"TMUX_MENU_SESSION_ID",
		"TMUX_MENU_SESSION_NAME",
		"TMUX_MENU_SESSION_PATH",
		"TMUX_MENU_STATUS_TITLE",
	} {
		out = append(out, name+"="+values[name])
	}
	return out
}

func normalizeStatusRadarBlocks(blocks []statusRadarBlock) error {
	if len(blocks) == 0 {
		return errors.New("reporter returned no blocks")
	}
	for i := range blocks {
		block := &blocks[i]
		block.Title = strings.TrimSpace(block.Title)
		block.Status = strings.ToLower(strings.TrimSpace(block.Status))
		block.Summary = strings.TrimSpace(block.Summary)
		block.Details = strings.TrimSpace(block.Details)
		if block.Title == "" {
			return fmt.Errorf("reporter blocks[%d].title is required", i)
		}
		switch block.Status {
		case "attention", "warning", "ok", "unknown":
		default:
			return fmt.Errorf("reporter blocks[%d].status must be one of attention, warning, ok, unknown", i)
		}
		if block.Summary == "" {
			return fmt.Errorf("reporter blocks[%d].summary is required", i)
		}
	}
	return nil
}

func failedStatusRadarReport(target config.StatusTarget, order int, message string) statusRadarReport {
	message = truncateStatusText(cleanStatusText(message), 240)
	block := statusRadarBlock{Title: "Reporter", Status: "unknown", Summary: message}
	return statusRadarReport{
		Target:  target,
		Blocks:  []statusRadarBlock{block},
		Status:  "unknown",
		Summary: message,
		Order:   order,
	}
}

func statusRadarRollup(blocks []statusRadarBlock) (string, string) {
	status := "ok"
	for _, block := range blocks {
		if statusRadarRank(block.Status) < statusRadarRank(status) {
			status = block.Status
		}
	}
	if status == "ok" {
		return status, "All checks passed"
	}

	summaries := make([]string, 0, len(blocks))
	for _, wanted := range []string{"attention", "warning", "unknown"} {
		for _, block := range blocks {
			if block.Status == wanted {
				summaries = append(summaries, cleanStatusText(block.Summary))
			}
		}
	}
	shown := summaries
	if len(shown) > 2 {
		shown = shown[:2]
	}
	summary := strings.Join(shown, " · ")
	if remaining := len(summaries) - len(shown); remaining > 0 {
		summary += fmt.Sprintf(" · +%d more", remaining)
	}
	return status, summary
}

func statusRadarRank(status string) int {
	switch status {
	case "attention":
		return 0
	case "warning":
		return 1
	case "unknown":
		return 2
	case "ok":
		return 3
	default:
		return 4
	}
}

func statusRadarItems(reports []statusRadarReport, previewDir string) ([]picker.Item[menuItem], error) {
	items := make([]picker.Item[menuItem], 0, len(reports))
	for i, report := range reports {
		previewPath := filepath.Join(previewDir, fmt.Sprintf("%03d.txt", i))
		if err := os.WriteFile(previewPath, []byte(statusRadarPreview(report)), 0o600); err != nil {
			return nil, fmt.Errorf("write status preview: %w", err)
		}
		item := picker.Item[menuItem]{
			Label:    statusRadarLabel(report),
			Preview:  previewPath,
			Disabled: !report.Available,
		}
		if report.Available {
			item.Value = menuItem{dispatch: action.SwitchSession(report.Session)}
		}
		items = append(items, item)
	}
	return items, nil
}

func statusRadarLabel(report statusRadarReport) string {
	return fmt.Sprintf("%s %s | %s",
		colorStatusRadarBadge(report.Status),
		colorPaneTitle(truncateStatusText(cleanStatusText(report.Target.Title), 30)),
		truncateStatusText(cleanStatusText(report.Summary), 90),
	)
}

func statusRadarPreview(report statusRadarReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n", colorPaneTitle(report.Target.Title))
	if report.Session.SessionPath != "" {
		fmt.Fprintf(&out, "%s\n", dim(shortenHome(report.Session.SessionPath)))
	}
	for _, block := range report.Blocks {
		fmt.Fprintf(&out, "\n%s %s\n%s\n", colorStatusRadarBadge(block.Status), block.Title, block.Summary)
		if block.Details != "" {
			fmt.Fprintf(&out, "\n%s\n", block.Details)
		}
	}
	return out.String()
}

func colorStatusRadarBadge(status string) string {
	switch status {
	case "attention":
		return ansiBold + ansiRed + "!" + ansiReset
	case "warning":
		return ansiYellow + "○" + ansiReset
	case "ok":
		return ansiGreen + "●" + ansiReset
	default:
		return ansiDim + "?" + ansiReset
	}
}

func statusRadarFooter() string {
	return "ATTENTION / WARNING / UNKNOWN / OK | Enter switch | Space preview | Ctrl-C cancel\n" + viewSwitchFooter()
}
