package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

const (
	updateReminderModeEnv     = "WAVE_UPDATE_REMINDER"
	updateReminderModeNever   = "never"
	updateCheckTimeout        = 1500 * time.Millisecond
	updateReminderDateLayout  = "2006-01-02"
	updateReminderStateFile   = "update-reminder.json"
	updateReminderStateSubdir = "wave"
)

type updateReminderState struct {
	CurrentVersion  string `json:"current_version"`
	LastCheckedDate string `json:"last_checked_date"`
	LastPrompted    string `json:"last_prompted_date"`
	PendingVersion  string `json:"pending_version"`
	PendingURL      string `json:"pending_release_url"`
}

func maybeRemindUpdate(ctx context.Context, cmd string) {
	if !shouldRemindForCommand(cmd) {
		return
	}
	if updateReminderDisabled() {
		return
	}

	current, err := versionForUpdate()
	if err != nil {
		return
	}

	statePath, err := updateReminderStatePath()
	if err != nil {
		return
	}
	state := loadUpdateReminderState(statePath)
	today := time.Now().Format(updateReminderDateLayout)

	if state.CurrentVersion != current {
		state = updateReminderState{CurrentVersion: current}
	}

	if state.LastCheckedDate != today {
		checkCtx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
		defer cancel()

		_, latest, found, checkErr := detectLatestRelease(checkCtx, false)
		state.LastCheckedDate = today
		state.LastPrompted = ""
		state.PendingVersion = ""
		state.PendingURL = ""
		if checkErr == nil && found && latest.GreaterThan(current) {
			state.PendingVersion = latest.Version()
			state.PendingURL = latest.URL
		}
		_ = saveUpdateReminderState(statePath, state)
		return
	}

	isNewer, valid := pendingVersionStatus(current, state.PendingVersion)
	if !valid {
		state.PendingVersion = ""
		state.PendingURL = ""
		_ = saveUpdateReminderState(statePath, state)
		return
	}
	if !isNewer || state.LastPrompted == today {
		return
	}

	fmt.Fprintf(os.Stderr, "info: wave update available: %s -> %s (run `wave update`)\n", current, state.PendingVersion)
	state.LastPrompted = today
	_ = saveUpdateReminderState(statePath, state)
}

func shouldRemindForCommand(cmd string) bool {
	switch cmd {
	case "index", "status", "search", "def", "refs", "context":
		return true
	default:
		return false
	}
}

func pendingVersionStatus(currentVersion string, pendingVersion string) (isNewer bool, valid bool) {
	current, err := semver.NewVersion(currentVersion)
	if err != nil {
		return false, false
	}
	pending, err := semver.NewVersion(strings.TrimSpace(pendingVersion))
	if err != nil {
		return false, false
	}
	return pending.GreaterThan(current), true
}

func updateReminderDisabled() bool {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(updateReminderModeEnv)))
	return mode == updateReminderModeNever
}

func updateReminderStatePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, updateReminderStateSubdir, updateReminderStateFile), nil
}

func loadUpdateReminderState(path string) updateReminderState {
	var state updateReminderState
	data, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return updateReminderState{}
	}
	return state
}

func saveUpdateReminderState(path string, state updateReminderState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}
