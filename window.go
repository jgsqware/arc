package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	_ "embed"

	"github.com/cli/go-gh/v2/pkg/tableprinter"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type Window struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

func NewCmdWindow() *cobra.Command {
	cmd := &cobra.Command{
		Short: "Manage windows",
		Use:   "window",
	}

	cmd.AddCommand(NewCmdWindowCreate())
	cmd.AddCommand(NewCmdWindowFocus())
	cmd.AddCommand(NewCmdWindowClose())
	cmd.AddCommand(NewCmdWindowList())

	return cmd
}

func NewCmdWindowCreate() *cobra.Command {
	var flags struct {
		Incognito bool
		Focus     string
	}

	cmd := &cobra.Command{
		Use:     "create [url]",
		Short:   "Create a new window",
		Aliases: []string{"new"},
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.Focus != "" {
				return windowCreateWithFocus(flags.Focus)
			}

			var applescript string
			if flags.Incognito {
				applescript = `tell application "Arc"
					make new window with properties {incognito:true}
					activate
				end tell`
			} else {
				applescript = `tell application "Arc"
					make new window
				end tell`
			}

			if _, err := runApplescript(applescript); err != nil {
				return err
			}

			if len(args) > 0 {
				if _, err := runApplescript(fmt.Sprintf(`tell application "Arc"
					tell front window
						make new tab with properties {URL:"%s"}
					end tell
				end tell`, args[0])); err != nil {
					return err
				}
			}

			if _, err := runApplescript(`tell application "Arc" to activate`); err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&flags.Incognito, "incognito", false, "open in incognito mode")
	cmd.Flags().StringVar(&flags.Focus, "focus", "", "focus the tab whose title contains this string")

	return cmd
}

func windowCreateWithFocus(search string) error {
	searchLower := strings.ToLower(search)

	// Try to find the tab in the existing front window first (idempotent path).
	if output, err := runApplescript(listTabsScript); err == nil {
		var tabs []Tab
		if json.Unmarshal(output, &tabs) == nil {
			if idx := findTab(tabs, searchLower); idx != -1 {
				_, _ = runApplescript(fmt.Sprintf(`tell application "Arc"
	tell front window
		tell tab %d to select
	end tell
	activate
end tell`, idx+1))
				return nil
			}
		}
	}

	// No existing window or no match — create a new window and wait for it to populate.
	if _, err := runApplescript(`tell application "Arc"
	make new window
	activate
	delay 3
end tell`); err != nil {
		return err
	}

	output, err := runApplescript(listTabsScript)
	if err != nil {
		return err
	}

	var tabs []Tab
	if err := json.Unmarshal(output, &tabs); err != nil {
		return err
	}

	if idx := findTab(tabs, searchLower); idx != -1 {
		if _, err := runApplescript(fmt.Sprintf(`tell application "Arc"
	tell front window
		tell tab %d to select
	end tell
	activate
end tell`, idx+1)); err != nil {
			_, _ = runApplescript(`tell application "Arc" to close front window`)
			return err
		}
		return nil
	}

	_, _ = runApplescript(`tell application "Arc" to close front window`)
	return fmt.Errorf("no tab found with title or URL containing %q", search)
}

func NewCmdWindowFocus() *cobra.Command {
	var flags struct {
		Create bool
	}

	cmd := &cobra.Command{
		Use:   "focus <search>",
		Short: "Focus a tab by title or URL in the current space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			search := strings.ToLower(args[0])
			hasArcWindow, err := hasArcWindowOnCurrentSpace()
			if err != nil {
				return fmt.Errorf("failed to query yabai: %w", err)
			}

			if hasArcWindow {
				// Arc window exists on this space — activate it and find the tab.
				if _, err := runApplescript(`tell application "Arc" to activate`); err != nil {
					return err
				}

				output, err := runApplescript(listTabsScript)
				if err != nil {
					return err
				}

				var tabs []Tab
				if err := json.Unmarshal(output, &tabs); err != nil {
					return err
				}

				if idx := findTab(tabs, search); idx != -1 {
					_, err := runApplescript(fmt.Sprintf(`tell application "Arc"
	tell front window
		tell tab %d to select
	end tell
	activate
end tell`, idx+1))
					return err
				}

				return fmt.Errorf("no tab found with title or URL containing %q", args[0])
			}

			if !flags.Create {
				return fmt.Errorf("no Arc window on current space (use --create to create one)")
			}

			// No Arc window on this space — create one.
			if _, err := runApplescript(`tell application "Arc"
	make new window
	activate
	delay 3
end tell`); err != nil {
				return err
			}

			output, err := runApplescript(listTabsScript)
			if err != nil {
				return err
			}

			var tabs []Tab
			if err := json.Unmarshal(output, &tabs); err != nil {
				return err
			}

			if idx := findTab(tabs, search); idx != -1 {
				if _, err := runApplescript(fmt.Sprintf(`tell application "Arc"
	tell front window
		tell tab %d to select
	end tell
	activate
end tell`, idx+1)); err != nil {
					_, _ = runApplescript(`tell application "Arc" to close front window`)
					return err
				}
				return nil
			}

			_, _ = runApplescript(`tell application "Arc" to close front window`)
			return fmt.Errorf("no tab found with title or URL containing %q", args[0])
		},
	}

	cmd.Flags().BoolVar(&flags.Create, "create", false, "create a new window if none exists on the current space")
	return cmd
}

// hasArcWindowOnCurrentSpace queries yabai to check if an Arc window exists on the focused space.
func hasArcWindowOnCurrentSpace() (bool, error) {
	output, err := exec.Command("yabai", "-m", "query", "--windows", "--space").Output()
	if err != nil {
		return false, err
	}

	var windows []struct {
		App string `json:"app"`
	}
	if err := json.Unmarshal(output, &windows); err != nil {
		return false, err
	}

	for _, w := range windows {
		if w.App == "Arc" {
			return true, nil
		}
	}
	return false, nil
}

// findTab returns the index of the first tab whose title or URL contains the search string.
// Returns -1 if no match is found.
func findTab(tabs []Tab, searchLower string) int {
	for i, tab := range tabs {
		if strings.Contains(strings.ToLower(tab.Title), searchLower) {
			return i
		}
	}
	for i, tab := range tabs {
		if strings.Contains(strings.ToLower(tab.URL), searchLower) {
			return i
		}
	}
	return -1
}

//go:embed applescript/list-windows.applescript
var listWindowsScript string

func NewCmdWindowList() *cobra.Command {
	flags := struct {
		Json bool
	}{}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List windows",
		RunE: func(cmd *cobra.Command, args []string) error {
			output, err := runApplescript(listWindowsScript)
			if err != nil {
				return err
			}

			var windows []Window
			if err := json.Unmarshal(output, &windows); err != nil {
				return err
			}

			if flags.Json {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				return encoder.Encode(windows)
			}

			var printer tableprinter.TablePrinter
			if !isatty.IsTerminal(os.Stdout.Fd()) {
				printer = tableprinter.New(os.Stdout, false, 0)
			} else {
				w, _, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil {
					return err
				}

				printer = tableprinter.New(os.Stdout, true, w)
			}

			printer.AddHeader([]string{"ID", "Title"})
			for _, window := range windows {
				printer.AddField(fmt.Sprintf("%d", window.ID))
				printer.AddField(window.Title)
				printer.EndRow()
			}

			return printer.Render()
		},
	}

	cmd.Flags().BoolVar(&flags.Json, "json", false, "output as json")
	return cmd
}

func NewCmdWindowClose() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "close",
		Aliases: []string{"remove", "rm"},
		Short:   "Close a window",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if _, err := runApplescript(`tell application "Arc" to tell front window to close`); err != nil {
					return err
				}
				return nil
			}

			for _, id := range args {
				windowID, err := strconv.Atoi(id)
				if err != nil {
					return err
				}

				if _, err := runApplescript(fmt.Sprintf(`tell application "Arc" to tell window %d to close`, windowID)); err != nil {
					return err
				}

			}
			return nil
		},
	}

	return cmd
}
