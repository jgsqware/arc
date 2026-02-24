package main

import (
	"encoding/json"
	"fmt"
	"os"
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
				return windowCreateWithFocus(flags.Incognito, flags.Focus)
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

func windowCreateWithFocus(incognito bool, search string) error {
	// Check if Arc is already running before we launch it
	wasRunning := true
	out, err := runApplescript(`application "Arc" is running`)
	if err == nil && strings.TrimSpace(string(out)) == "false" {
		wasRunning = false
	}

	makeWindow := `make new window`
	if incognito {
		makeWindow = `make new window with properties {incognito:true}`
	}

	// Escape the search string for AppleScript
	escaped := strings.ReplaceAll(search, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)

	applescript := fmt.Sprintf(`tell application "Arc"
	%s
	delay 1
	set maxRetries to 10
	repeat with attempt from 1 to maxRetries
		tell front window
			set tabIndex to 1
			repeat with aTab in every tab
				try
					set tabTitle to title of aTab
					ignoring case
						if tabTitle contains "%s" then
							tell tab tabIndex to select
							activate
							return "found"
						end if
					end ignoring
				end try
				set tabIndex to tabIndex + 1
			end repeat
		end tell
		if attempt < maxRetries then delay 0.5
	end repeat
	activate
	return "not_found"
end tell`, makeWindow, escaped)

	output, err := runApplescript(applescript)
	if err != nil {
		return err
	}

	// If Arc was not running, it opens startup windows alongside ours.
	// Close all windows except the front one (which is the one we just created).
	if !wasRunning {
		if _, err := runApplescript(`tell application "Arc"
	set windowCount to count of windows
	repeat with i from windowCount to 2 by -1
		close window i
	end repeat
end tell`); err != nil {
			return err
		}
	}

	if strings.TrimSpace(string(output)) == "not_found" {
		return fmt.Errorf("no tab found with title containing %q", search)
	}

	return nil
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
