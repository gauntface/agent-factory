package api

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sachiniyer/agent-factory/backlog"
	"github.com/sachiniyer/agent-factory/daemon"
)

func backlogStatusCmd(use, short, long string, to backlog.Status) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <id>",
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBacklog(func() (any, error) {
				id, err := backlog.ParseID(args[0])
				if err != nil {
					return nil, err
				}
				t, err := backlogProject()
				if err != nil {
					return nil, err
				}
				var resp daemon.SetBacklogItemStatusResponse
				if err := callBacklogRoute("SetBacklogItemStatus", daemon.SetBacklogItemStatusRequest{
					RepoID: t.repoID, RepoPath: t.repoPath, ID: id, Status: to,
				}, &resp); err != nil {
					return nil, err
				}
				return resp.Item, nil
			})
		},
	}
}

var (
	backlogQueueCmd = backlogStatusCmd("queue", "Make an item eligible for the runner",
		"Set the item's status to queued. On a parent, queue its draft children.", backlog.StatusQueued)
	backlogDraftCmd = backlogStatusCmd("draft", "Put a queued or in-review item back to draft",
		"Set the item's status to draft, so the runner does not start it.", backlog.StatusDraft)
	backlogReviewCmd = backlogStatusCmd("review", "Send a running item to review",
		"Set a running item's status to review, for you to check and mark done.", backlog.StatusReview)
	backlogReopenCmd = backlogStatusCmd("reopen", "Reopen a done or dropped item as a draft",
		"Set a done or dropped item's status back to draft.", backlog.StatusDraft)
	backlogDoneCmd = backlogStatusCmd("done", "Mark an item done after review",
		"Mark the item done. Items that wait on it can then start. Open questions on it close; "+
			"its session stays.", backlog.StatusDone)
	backlogDropCmd = backlogStatusCmd("drop", "Mark an item dropped",
		"Mark the item dropped. On a parent, drop its open children. Its session stays.", backlog.StatusDropped)
)

var backlogDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a draft that has no runs and no thread",
	Long:  "Delete a draft item that has no runs, no thread, and no children. To remove any other item, drop it.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBacklog(func() (any, error) {
			id, err := backlog.ParseID(args[0])
			if err != nil {
				return nil, err
			}
			t, err := backlogProject()
			if err != nil {
				return nil, err
			}
			var resp daemon.DeleteBacklogItemResponse
			if err := callBacklogRoute("DeleteBacklogItem", daemon.DeleteBacklogItemRequest{
				RepoID: t.repoID, RepoPath: t.repoPath, ID: id,
			}, &resp); err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "deleted": true}, nil
		})
	},
}

var backlogNoteCmd = &cobra.Command{
	Use:   "note <id> <text>",
	Short: "Add a note to an item's thread",
	Long:  "Add a note to the item's thread. A note is not delivered to the item's session.",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBacklog(func() (any, error) {
			id, err := backlog.ParseID(args[0])
			if err != nil {
				return nil, err
			}
			t, err := backlogProject()
			if err != nil {
				return nil, err
			}
			var resp daemon.PostBacklogMessageResponse
			if err := callBacklogRoute("PostBacklogMessage", daemon.PostBacklogMessageRequest{
				RepoID: t.repoID, RepoPath: t.repoPath, ID: id, Kind: backlog.KindNote,
				Text: strings.Join(args[1:], " "),
			}, &resp); err != nil {
				return nil, err
			}
			return resp.Message, nil
		})
	},
}

var (
	backlogRunnerMaxRunning int
	backlogRunnerOnDone     string
)

// backlogRunnerView is what af backlog runner prints.
type backlogRunnerView struct {
	backlog.RunnerView
	Runner     bool           `json:"runner"`
	MaxRunning int            `json:"max_running"`
	OnDone     string         `json:"on_done"`
	Budget     backlog.Budget `json:"budget"`
}

var backlogRunnerCmd = &cobra.Command{
	Use:   "runner [on | off] [--max-running N] [--on-done keep|archive]",
	Short: "Show or change this project's runner",
	Long: "With no arguments, print the runner's state and reason, its slots, and the budget. " +
		"on and off switch the runner for this project; it is off by default. --max-running " +
		"sets how many items run at once (1-8). --on-done sets what happens to an item's " +
		"session when it is done: keep it or archive it.",
	Args:      cobra.MaximumNArgs(1),
	ValidArgs: []string{"on", "off"},
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBacklog(func() (any, error) {
			var (
				runner, changed bool
				maxRunning      *int
				onDone          *string
				runnerSet       *bool
			)
			if len(args) == 1 {
				switch args[0] {
				case "on":
					runner = true
				case "off":
				default:
					return nil, fmt.Errorf("invalid runner switch %q: use on or off", args[0])
				}
				runnerSet, changed = &runner, true
			}
			if cmd.Flags().Changed("max-running") {
				maxRunning, changed = &backlogRunnerMaxRunning, true
			}
			if cmd.Flags().Changed("on-done") {
				onDone, changed = &backlogRunnerOnDone, true
			}
			t, err := backlogProject()
			if err != nil {
				return nil, err
			}
			if changed {
				var resp daemon.SetBacklogSettingsResponse
				if err := callBacklogRoute("SetBacklogSettings", daemon.SetBacklogSettingsRequest{
					RepoID: t.repoID, RepoPath: t.repoPath, Runner: runnerSet, MaxRunning: maxRunning, OnDone: onDone,
				}, &resp); err != nil {
					return nil, err
				}
			}
			v, err := t.get()
			if err != nil {
				return nil, err
			}
			var budget daemon.GetBudgetResponse
			if err := callBacklogRoute("GetBudget", daemon.GetBudgetRequest{}, &budget); err != nil {
				return nil, err
			}
			s := v.Backlog.Settings
			return backlogRunnerView{RunnerView: v.Runner, Runner: s.Runner, MaxRunning: s.MaxRunning,
				OnDone: s.OnDone, Budget: budget.Budget}, nil
		})
	},
}

var backlogBudgetMaxPerRun string

var backlogBudgetCmd = &cobra.Command{
	Use:   "budget [add <usd> | set <usd> | reset] [--max-per-run <usd>]",
	Short: "Show or change the global budget",
	Long: "The budget is money, in US dollars at API list prices, that the runner may spend " +
		"before it stops. It is one budget for all projects. With no arguments, print it. add " +
		"raises the limit, set replaces it, reset sets spent back to $0. --max-per-run caps one " +
		"run; 0 means no cap.",
	Args:      cobra.MaximumNArgs(2),
	ValidArgs: []string{"add", "set", "reset"},
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBacklog(func() (any, error) {
			req, change, err := budgetRequest(args)
			if err != nil {
				return nil, err
			}
			if cmd.Flags().Changed("max-per-run") {
				v, err := parseUSD(backlogBudgetMaxPerRun)
				if err != nil {
					return nil, err
				}
				req.MaxPerRun, change = &v, true
			}
			if !change {
				var resp daemon.GetBudgetResponse
				if err := callBacklogRoute("GetBudget", daemon.GetBudgetRequest{}, &resp); err != nil {
					return nil, err
				}
				return resp.Budget, nil
			}
			var resp daemon.SetBudgetResponse
			if err := callBacklogRoute("SetBudget", req, &resp); err != nil {
				return nil, err
			}
			return resp.Budget, nil
		})
	},
}

func budgetRequest(args []string) (daemon.SetBudgetRequest, bool, error) {
	var req daemon.SetBudgetRequest
	if len(args) == 0 {
		return req, false, nil
	}
	switch args[0] {
	case "reset":
		if len(args) != 1 {
			return req, false, errors.New("reset takes no amount")
		}
		req.Reset = true
		return req, true, nil
	case "add", "set":
		if len(args) != 2 {
			return req, false, fmt.Errorf("%s needs an amount in US dollars, for example \"af backlog budget %s 5\"", args[0], args[0])
		}
		v, err := parseUSD(args[1])
		if err != nil {
			return req, false, err
		}
		if args[0] == "add" {
			req.Add = &v
		} else {
			req.Set = &v
		}
		return req, true, nil
	}
	return req, false, fmt.Errorf("unknown budget action %q: use add, set or reset", args[0])
}

func parseUSD(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(s), "$"), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q: use US dollars, for example 5 or 2.50", s)
	}
	return v, nil
}

func init() {
	backlogRunnerCmd.Flags().IntVar(&backlogRunnerMaxRunning, "max-running", backlog.DefaultMaxRunning, "How many items may run at once (1-8)")
	backlogRunnerCmd.Flags().StringVar(&backlogRunnerOnDone, "on-done", backlog.OnDoneKeep, "What happens to an item's session when it is done: keep or archive")
	backlogBudgetCmd.Flags().StringVar(&backlogBudgetMaxPerRun, "max-per-run", "", "Cap one run at this many US dollars; 0 means no cap")

	BacklogCmd.AddCommand(backlogQueueCmd, backlogDraftCmd, backlogReviewCmd, backlogReopenCmd, backlogDoneCmd,
		backlogDropCmd, backlogDeleteCmd, backlogNoteCmd, backlogRunnerCmd, backlogBudgetCmd)
}
