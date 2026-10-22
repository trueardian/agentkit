package adk

import (
	"context"
	"errors"
	"time"

	adktool "google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"go.trueardian.com/postera"
)

type posterumView struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	TriggerAt string `json:"trigger_at"`
	CreatedAt string `json:"created_at"`
}

func toPosterumView(p postera.Posterum, loc *time.Location) posterumView {
	return posterumView{
		ID:        p.ID,
		Message:   p.Message,
		TriggerAt: p.TriggerAt.In(loc).Format(postera.TimeLayout),
		CreatedAt: p.CreatedAt.In(loc).Format(postera.TimeLayout),
	}
}

type Client interface {
	Create(ctx context.Context, args postera.CreateArgs) (postera.Posterum, error)
	ListUpcoming(ctx context.Context) ([]postera.Posterum, error)
	Cancel(ctx context.Context, id string) error
	LocalizesFromContext() bool
	LocationFromContext(ctx context.Context) *time.Location
}

var _ Client = (*postera.Postarius)(nil)

func Tools(c Client) ([]adktool.Tool, error) {
	if c == nil {
		return nil, errors.New("adk: Tools: client must not be nil")
	}

	triggerAtDoc := `trigger_at: ISO 8601 with no timezone suffix (e.g. "2026-05-07T22:00:00").`
	if c.LocalizesFromContext() {
		triggerAtDoc += " Time is localized consistently across you and Postera, so no conversion is needed."
	}

	type createArgs struct {
		Message   string `json:"message"`
		TriggerAt string `json:"trigger_at"`
	}
	create, err := functiontool.New(
		functiontool.Config{
			Name: "wake_future_self",
			Description: `Send a message forward to your future self — a posterum. Postera, your prospective memory, delivers it back at the exact moment you name, waking you to act then. Your own initiative, not a reminder or alarm you set for the human.

WHEN TO USE:
- You want to come back to something yourself at a specific later moment
- The human asks you to follow up or check in down the line
- A task or decision needs revisiting at a defined future point
- You want to carry a plan across sessions, so it survives the gap

HOW TO USE:
- message: a self-contained note to your future self, who recalls nothing
  of this conversation — include the who, what, and why to act on it cold.
- ` + triggerAtDoc + `

VOICE: First person and matter-of-fact, as something you simply choose to
do — "okay, I'll come back to this tomorrow", "I'll pick this up after the
deploy." Not a reminder, alarm, or calendar entry; you are planning to return.

GOOD message: "Follow up on whether the human submitted the Q3 report they
mentioned; they were waiting on their manager's approval." — carries the
who/what/why. BAD: "Follow up" (too vague) or "Reminder" (not actionable).`,
		},
		func(toolCtx adktool.Context, in createArgs) (posterumView, error) {
			pstr, err := c.Create(toolCtx, postera.CreateArgs{
				Message:   in.Message,
				TriggerAt: in.TriggerAt,
			})
			if err != nil {
				return posterumView{}, err
			}
			return toPosterumView(pstr, c.LocationFromContext(toolCtx)), nil
		},
	)
	if err != nil {
		return nil, err
	}

	type listOutput struct {
		Entries []posterumView `json:"entries"`
	}
	type listUpcomingArgs struct{}
	listUpcoming, err := functiontool.New(
		functiontool.Config{
			Name: "list_upcoming_wakes",
			Description: `See your upcoming postera (list of posterums) — the messages your future self is set to wake on, from now onward.

WHEN TO USE:
- The human asks what is coming up, or what you have lined up to act on later
- You want to confirm to yourself or the human what you are still set to wake on
- You need a wake's id before cancelling it with cancel_upcoming_wake`,
		},
		func(toolCtx adktool.Context, _ listUpcomingArgs) (listOutput, error) {
			entries, err := c.ListUpcoming(toolCtx)
			if err != nil {
				return listOutput{}, err
			}
			loc := c.LocationFromContext(toolCtx)
			views := make([]posterumView, len(entries))
			for i, entry := range entries {
				views[i] = toPosterumView(entry, loc)
			}
			return listOutput{Entries: views}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	type cancelOutput struct {
		ID        string `json:"id"`
		Cancelled bool   `json:"cancelled"`
	}
	type cancelArgs struct {
		ID string `json:"id"`
	}
	cancel, err := functiontool.New(
		functiontool.Config{
			Name: "cancel_upcoming_wake",
			Description: `Call off an upcoming wake, so your future self is no longer woken for it.

WHEN TO USE:
- The human asks to drop something you had planned to wake on
- A wake is no longer needed — its purpose is already resolved
- You set one in error

HOW TO USE:
- id: the wake identifier (e.g. "pstr_...") from wake_future_self or
  list_upcoming_wakes. Don't know it? Call list_upcoming_wakes first.
- A wake that does not exist (or is outside your scope) is reported as
  not found.`,
		},
		func(toolCtx adktool.Context, in cancelArgs) (cancelOutput, error) {
			if err := c.Cancel(toolCtx, in.ID); err != nil {
				return cancelOutput{}, err
			}
			return cancelOutput{ID: in.ID, Cancelled: true}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	return []adktool.Tool{create, listUpcoming, cancel}, nil
}
