package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"tmux-menu/internal/action"
	"tmux-menu/internal/agenthud"
	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/tmux"
)

const (
	agentHUDRefreshInterval   = time.Second
	agentHUDGenerationTimeout = 900 * time.Millisecond
	agentHUDMaxVisiblePanes   = 4
	agentHUDCaptureLines      = 200
	agentHUDCaptureBytes      = 256 << 10
	// Inventory and post-capture identity validation may consume 4 MiB each
	// for two pane lists plus process data. Reserve another 4*256 KiB for the
	// maximum visible capture set; hook/config reads share this same cap.
	agentHUDTotalOutputBytes = 13 << 20
	agentHUDTerminalBytes    = 256 << 10
	agentHUDTerminalWidth    = 240
	agentHUDTerminalHeight   = 200
	agentHUDFailureWidth     = 160
)

var (
	runAgentHUDProgram    = agenthud.Run
	loadAgentHUDInventory = loadAgentInventoryBoundedReadOnly
	captureAgentHUDPane   = tmux.CapturePaneBounded
	listAgentHUDPanes     = tmux.ListPanesBounded
	agentHUDClock         = time.Now
)

func runAgentHUD(ctx context.Context) error {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	theme, err := agentHUDTheme(cfg.Agents)
	if err != nil {
		return err
	}
	model := agenthud.NewModel(agenthud.ModelOptions{
		Width: 80, Height: 24, Now: agentHUDClock(), MaxTerminalBytes: agentHUDTerminalBytes, Theme: theme,
	})
	refresh := newAgentHUDRefresh(cfg, rt, newAgentHUDCoordinator())
	return runAgentHUDSession(ctx, model, refresh)
}

func runAgentHUDSession(ctx context.Context, model agenthud.Model, refresh agenthud.RefreshFunc) error {
	for {
		result, err := runAgentHUDProgram(ctx, model, refresh, agenthud.RuntimeOptions{
			RefreshInterval: agentHUDRefreshInterval,
			Clock:           agentHUDClock,
		})
		if err != nil {
			return err
		}
		model = result.Model
		switch result.Action.Kind() {
		case agenthud.ActionQuit, agenthud.ActionNone:
			return nil
		case agenthud.ActionSwitch:
			return dispatch(ctx, agentHUDSwitchDispatch(result.Action.Identity()))
		case agenthud.ActionOpenPicker:
			canceled, err := runHUDPickerSubflow(ctx, model.SelectedPaneID())
			if err != nil {
				return err
			}
			if !canceled {
				return nil
			}
		default:
			return fmt.Errorf("unknown agent HUD action %d", result.Action.Kind())
		}
	}
}

func runHUDPickerSubflow(ctx context.Context, initialPaneID string) (bool, error) {
	for {
		result, err := selectModeForLoop(ctx, "agents", initialPaneID)
		if errors.Is(err, picker.ErrCanceled) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if result.Key == "ctrl-r" || result.Key == "ctrl-x" {
			if result.Selected && result.Value.agentPaneID != "" {
				initialPaneID = result.Value.agentPaneID
			}
			if result.Key == "ctrl-x" && result.Selected && result.Value.agentAckToken != "" {
				if err := acknowledgeAgent(ctx, result.Value.agentAckToken); err != nil && !errors.Is(err, agentstatus.ErrNotAcknowledgeable) {
					return false, err
				}
			}
			continue
		}
		next := viewModeForKey(result.Key)
		if next != "" || result.Key == "tab" || result.Key == "btab" {
			cfg, _, err := loadConfig(ctx)
			if err != nil {
				return false, err
			}
			if next == "" {
				next = tabViewMode("agents", result.Key, cfg.Picker.TabOrder)
			}
			if os.Getenv("TMUX_MENU_DISPATCH_FILE") != "" {
				return false, dispatch(ctx, action.Dispatch{Mode: popupViewDispatchMode, Cmd: next})
			}
			if next == "agents" {
				return true, nil
			}
			return false, runPickerLoop(ctx, next)
		}
		if !result.Selected {
			return true, nil
		}
		return false, dispatch(ctx, dispatchForResult(result))
	}
}

func agentHUDSwitchDispatch(identity agenthud.Identity) action.Dispatch {
	return action.Dispatch{
		Mode: "switch-pane", SessionID: identity.SessionID(), WindowID: identity.WindowID(), PaneID: identity.PaneID(),
		PanePID: identity.PanePID(), ProviderPID: identity.ProviderPID(),
	}
}

func newAgentHUDCoordinator() *agenthud.Coordinator {
	return agenthud.NewCoordinator(agenthud.GenerationLimits{
		Timeout:             agentHUDGenerationTimeout,
		MaxVisiblePanes:     agentHUDMaxVisiblePanes,
		CaptureLines:        agentHUDCaptureLines,
		CaptureBytesPerPane: agentHUDCaptureBytes,
		TotalOutputBytes:    agentHUDTotalOutputBytes,
		MaxTerminalBytes:    agentHUDTerminalBytes,
		Terminal: agenthud.TerminalLimits{
			Width: agentHUDTerminalWidth, Height: agentHUDTerminalHeight,
			MaxInputBytes: agentHUDCaptureBytes, MaxRetainedBytes: agentHUDCaptureBytes,
		},
	})
}

func newAgentHUDRefresh(cfg config.Config, rt runtimeContext, coordinator *agenthud.Coordinator) agenthud.RefreshFunc {
	return func(ctx context.Context, request agenthud.RefreshRequest) (agenthud.RefreshResult, error) {
		generation, ok := coordinator.Begin(ctx)
		if !ok {
			return agenthud.RefreshResult{}, errors.New("agent HUD refresh is already active")
		}
		defer generation.Done()

		now := agentHUDClock()
		inventory, err := loadAgentHUDInventory(generation.Context(), cfg.Session.Color, now, generation.OutputBudget())
		if err != nil {
			return agenthud.RefreshResult{}, err
		}
		agents, live := agentHUDProjection(inventory, rt.OriginPane)
		result := agenthud.RefreshResult{Generation: request.Generation, Now: now, Agents: agents}

		captureIDs := make([]string, 0, len(request.Targets))
		validTargets := make(map[string]agenthud.Identity, len(request.Targets))
		for _, target := range request.Targets {
			current, exists := live[target.Identity.PaneID()]
			if !exists || current != target.Identity {
				continue
			}
			captureIDs = append(captureIDs, current.PaneID())
			validTargets[current.PaneID()] = current
		}
		captured, err := generation.CaptureVisible(captureIDs, captureAgentHUDPane)
		if err != nil {
			return agenthud.RefreshResult{}, err
		}
		postCaptureLive := make(map[string]bool, len(captureIDs))
		if len(captureIDs) > 0 {
			panes, err := listAgentHUDPanes(generation.Context(), generation.OutputBudget(), tmux.DefaultPaneListLimits())
			if err != nil {
				return agenthud.RefreshResult{}, err
			}
			for _, pane := range panes {
				identity, exists := validTargets[pane.PaneID]
				panePID, parseErr := strconv.Atoi(pane.PanePID)
				if exists && parseErr == nil && pane.SessionID == identity.SessionID() && pane.WindowID == identity.WindowID() && panePID == identity.PanePID() {
					postCaptureLive[pane.PaneID] = true
				}
			}
		}
		byPane := make(map[string]agenthud.CaptureResult, len(captured))
		for _, capture := range captured {
			byPane[capture.PaneID] = capture
		}
		for _, target := range request.Targets {
			identity, valid := validTargets[target.Identity.PaneID()]
			if !valid || identity != target.Identity || !postCaptureLive[identity.PaneID()] {
				result.Captures = append(result.Captures, agenthud.TerminalUpdate{
					Identity: target.Identity, Failed: true,
					Failure: agenthud.SanitizeText("pane identity changed or vanished", agentHUDFailureWidth),
				})
				continue
			}
			capture := byPane[identity.PaneID()]
			update := agenthud.TerminalUpdate{Identity: identity, Terminal: capture.Terminal}
			if capture.Err != nil {
				update.Failed = true
				update.Failure = agenthud.SanitizeText(capture.Err.Error(), agentHUDFailureWidth)
			}
			result.Captures = append(result.Captures, update)
		}
		return result, nil
	}
}

func agentHUDProjection(inventory agentInventory, currentPaneID string) ([]agenthud.Agent, map[string]agenthud.Identity) {
	agents := make([]agenthud.Agent, 0, len(inventory.rows))
	live := make(map[string]agenthud.Identity, len(inventory.rows))
	for _, row := range inventory.rows {
		panePID, err := strconv.Atoi(row.pane.PanePID)
		if err != nil {
			continue
		}
		identity, err := agenthud.NewIdentity(row.pane.SessionID, row.pane.WindowID, row.pane.PaneID, panePID, row.providerPID)
		if err != nil {
			continue
		}
		provider := nonEmpty(string(row.provider), row.name)
		if provider == "" {
			provider = "agent"
		}
		sessionColor, err := agenthud.ParseColor(sessionColorForPane(row.pane, inventory.sessionColors))
		if err != nil {
			sessionColor = agenthud.ColorDefault
		}
		agent := agenthud.NewAgent(agenthud.AgentPresentation{
			Identity: identity, ProviderKind: row.provider, Provider: agenthud.SanitizeText(provider, 32),
			Status: row.status, Session: agenthud.SanitizeText(shortUUID(row.pane.SessionName), 96),
			Thread:  agenthud.SanitizeText(agentListPaneTitle(row.pane, row.name), 256),
			Workdir: agenthud.SanitizeText(agentListWorkdir(row.pane.CurrentPath), 512),
			Source:  agenthud.SanitizeText(row.statusSource, 64), Fresh: row.fresh, SessionColor: sessionColor,
			CurrentThread: row.pane.PaneID == currentPaneID, Children: len(row.children),
			TurnStartedAt: row.turnStartedAt, StateChangedAt: row.stateChangedAt, LastEventAt: row.lastEventAt,
		})
		agents = append(agents, agent)
		live[identity.PaneID()] = identity
	}
	return agents, live
}

func agentHUDTheme(value config.AgentsConfig) (agenthud.Theme, error) {
	return agenthud.NewTheme(agenthud.ThemeConfig{
		CodexIcon: value.Icons.Codex, ClaudeIcon: value.Icons.Claude, OtherIcon: value.Icons.Other, CurrentIcon: value.Icons.Current,
		AttentionIcon: value.Icons.Attention, WorkingIcon: value.Icons.Working, CompletedIcon: value.Icons.Completed,
		WaitingIcon: value.Icons.Waiting, UnknownIcon: value.Icons.Unknown,
		CodexColor: value.Colors.Codex, ClaudeColor: value.Colors.Claude, OtherColor: value.Colors.Other,
		ThreadColor: value.Colors.Thread, WorkdirColor: value.Colors.Workdir,
		AttentionColor: value.Colors.Attention, WorkingColor: value.Colors.Working, CompletedColor: value.Colors.Completed,
		WaitingColor: value.Colors.Waiting, UnknownColor: value.Colors.Unknown,
	})
}
