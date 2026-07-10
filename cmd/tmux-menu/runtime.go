package main

import (
	"context"
	"fmt"
	"os"

	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	"tmux-menu/internal/tmux"
)

var displayTmux = tmux.Display

type runtimeContext struct {
	OriginPane  string
	OriginPath  string
	SessionID   string
	SessionName string
	SessionPath string
}

func loadRuntimeContext(ctx context.Context) (runtimeContext, error) {
	originPane, err := loadRuntimeValue(ctx, "TMUX_MENU_ORIGIN_PANE", "#{pane_id}")
	if err != nil {
		return runtimeContext{}, err
	}
	originPath, err := loadRuntimeValue(ctx, "TMUX_MENU_ORIGIN_PATH", "#{pane_current_path}")
	if err != nil {
		return runtimeContext{}, err
	}
	sessionID, err := loadRuntimeValue(ctx, "TMUX_MENU_SESSION_ID", "#{session_id}")
	if err != nil {
		return runtimeContext{}, err
	}
	sessionName, err := loadRuntimeValue(ctx, "TMUX_MENU_SESSION_NAME", "#{session_name}")
	if err != nil {
		return runtimeContext{}, err
	}
	sessionPath, err := loadRuntimeValue(ctx, "TMUX_MENU_SESSION_PATH", "#{session_path}")
	if err != nil {
		return runtimeContext{}, err
	}
	return runtimeContext{
		OriginPane:  originPane,
		OriginPath:  originPath,
		SessionID:   sessionID,
		SessionName: sessionName,
		SessionPath: sessionPath,
	}, nil
}

func loadRuntimeValue(ctx context.Context, envName string, format string) (string, error) {
	if value := os.Getenv(envName); value != "" {
		return value, nil
	}
	value, err := displayTmux(ctx, format)
	if err != nil {
		return "", fmt.Errorf("load tmux context %s: %w", format, err)
	}
	if value == "" {
		return "", fmt.Errorf("load tmux context %s: empty result", format)
	}
	return value, nil
}

func loadConfig(ctx context.Context) (config.Config, runtimeContext, error) {
	rt, err := loadRuntimeContext(ctx)
	if err != nil {
		return config.Config{}, runtimeContext{}, err
	}
	cfg, err := config.LoadForContext(rt.OriginPath, rt.SessionPath)
	return cfg, rt, err
}

func dispatch(ctx context.Context, d action.Dispatch) error {
	dispatchFile := os.Getenv("TMUX_MENU_DISPATCH_FILE")
	if dispatchFile != "" {
		return action.Write(dispatchFile, d)
	}
	return action.Execute(ctx, d, os.Getenv("TMUX_MENU_ORIGIN_PANE"))
}
