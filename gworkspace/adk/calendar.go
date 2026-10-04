// Package adk exposes Google Workspace clients — Calendar, Gmail,
// Contacts — as ADK tools, giving an agent the ability to view and manage a
// user's schedule, email, and contacts.
package adk

import (
	"context"
	"errors"
	"fmt"
	"time"

	adktool "google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"go.trueardian.com/gworkspace"
)

type eventView struct {
	ID          string   `json:"id"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Location    string   `json:"location,omitempty"`
	Start       string   `json:"start"` // RFC3339
	End         string   `json:"end"`   // RFC3339
	Attendees   []string `json:"attendees,omitempty"`
	HTMLLink    string   `json:"html_link"`
}

func toEventView(e gworkspace.Event) eventView {
	return eventView{
		ID:          e.ID,
		Summary:     e.Summary,
		Description: e.Description,
		Location:    e.Location,
		Start:       e.Start.Format(time.RFC3339),
		End:         e.End.Format(time.RFC3339),
		Attendees:   e.Attendees,
		HTMLLink:    e.HTMLLink,
	}
}

// CalendarClient is the surface required by the Calendar toolset.
// *gworkspace.Calendar satisfies this interface implicitly.
type CalendarClient interface {
	GetEvents(ctx context.Context, ownerID string, q gworkspace.EventQuery) ([]gworkspace.Event, error)
	AddEvent(ctx context.Context, ownerID string, in gworkspace.EventInput) (gworkspace.Event, error)
}

// CalendarTools returns the Google Calendar toolset bound to c. It errors if
// c is nil.
func CalendarTools(c CalendarClient) ([]adktool.Tool, error) {
	if c == nil {
		return nil, errors.New("adk: CalendarTools: client must not be nil")
	}
	type eventsOutput struct {
		Events []eventView `json:"events"`
	}
	type getEventsArgs struct {
		Query   string `json:"query,omitempty"`
		TimeMin string `json:"time_min,omitempty"` // RFC3339, default now if empty
		TimeMax string `json:"time_max,omitempty"` // RFC3339, open if empty
		Limit   int    `json:"limit,omitempty"`
	}
	getEvents, err := functiontool.New(
		functiontool.Config{
			Name: "get_events",
			Description: `WHEN TO USE:
- The human asks for their schedule, agenda, or a list of events
- Before creating a new event, to check for time conflicts
- To find events by keyword, time range, or attendee

HOW TO USE:
- query: free-text search (event name, location, etc.). Leave empty for no text filter.
- time_min / time_max: time filter, RFC3339 format. time_min defaults to now if empty.
- limit: maximum events to return. Leave empty for the Google default.

WHAT I GET BACK:
- A list of events with id, summary, description, location, start, end, attendees, html_link.
  Use html_link to reference an event, id for follow-up operations.`,
		},
		func(toolCtx adktool.Context, in getEventsArgs) (eventsOutput, error) {
			var timeMin, timeMax time.Time
			var err error

			if in.TimeMin != "" {
				timeMin, err = time.Parse(time.RFC3339, in.TimeMin)
				if err != nil {
					return eventsOutput{}, fmt.Errorf("invalid time_min format (must be RFC3339): %w", err)
				}
			}
			if in.TimeMax != "" {
				timeMax, err = time.Parse(time.RFC3339, in.TimeMax)
				if err != nil {
					return eventsOutput{}, fmt.Errorf("invalid time_max format (must be RFC3339): %w", err)
				}
			}
			q := gworkspace.EventQuery{
				Query:   in.Query,
				TimeMin: timeMin,
				TimeMax: timeMax,
				Limit:   in.Limit,
			}
			events, err := c.GetEvents(toolCtx, toolCtx.UserID(), q)
			if err != nil {
				if errors.Is(err, gworkspace.ErrNotConnected) {
					return eventsOutput{}, errors.New("the human hasn't connected their Google account yet — direct them to the OAuth flow before accessing Calendar")
				}
				return eventsOutput{}, err
			}

			views := make([]eventView, len(events))
			for i, e := range events {
				views[i] = toEventView(e)
			}
			return eventsOutput{Events: views}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	type addEventArgs struct {
		Summary     string   `json:"summary"`
		Start       string   `json:"start"` // RFC3339
		End         string   `json:"end"`   // RFC3339
		Description string   `json:"description,omitempty"`
		Location    string   `json:"location,omitempty"`
		Guests      []string `json:"guests,omitempty"`
	}
	addEvent, err := functiontool.New(
		functiontool.Config{
			Name: "add_event",
			Description: `WHEN TO USE:
- The human asks to create or schedule an event / meeting / appointment
- Confirm the details (time, title, guests) before creating if not already clear

HOW TO USE:
- summary: event title (required)
- start / end: start and end times, RFC3339 format (required)
- description: event notes (optional)
- location: location (optional)
- guests: list of guest emails (optional)

WHAT I GET BACK:
- The created event, including html_link to share back with the human.`,
		},
		func(toolCtx adktool.Context, in addEventArgs) (eventView, error) {
			if in.Summary == "" {
				return eventView{}, errors.New("summary is required")
			}
			if in.Start == "" {
				return eventView{}, errors.New("start time is required")
			}
			if in.End == "" {
				return eventView{}, errors.New("end time is required")
			}

			startTime, err := time.Parse(time.RFC3339, in.Start)
			if err != nil {
				return eventView{}, fmt.Errorf("invalid start time format (must be RFC3339): %w", err)
			}

			endTime, err := time.Parse(time.RFC3339, in.End)
			if err != nil {
				return eventView{}, fmt.Errorf("invalid end time format (must be RFC3339): %w", err)
			}

			input := gworkspace.EventInput{
				Summary:     in.Summary,
				Start:       startTime,
				End:         endTime,
				Description: in.Description,
				Location:    in.Location,
				Guests:      in.Guests,
			}

			created, err := c.AddEvent(toolCtx, toolCtx.UserID(), input)
			if err != nil {
				if errors.Is(err, gworkspace.ErrNotConnected) {
					return eventView{}, errors.New("the human hasn't connected their Google account yet — direct them to the OAuth flow before accessing Calendar")
				}
				return eventView{}, err
			}

			return toEventView(created), nil
		},
	)
	if err != nil {
		return nil, err
	}

	return []adktool.Tool{
		getEvents,
		addEvent,
	}, nil
}
