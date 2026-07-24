package adk

import (
	"context"
	"errors"
	"time"

	adktool "google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"go.naturallyfunny.dev/gworkspace"
)

type msgView struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Subject  string `json:"subject"`
	Snippet  string `json:"snippet"`
	Body     string `json:"body,omitempty"`
	Date     string `json:"date"` // RFC3339
}

func toMsgView(m gworkspace.Message) msgView {
	return msgView{
		ID:       m.ID,
		ThreadID: m.ThreadID,
		From:     m.From,
		To:       m.To,
		Subject:  m.Subject,
		Snippet:  m.Snippet,
		Body:     m.Body,
		Date:     m.Date.Format(time.RFC3339),
	}
}

// GmailClient is the surface required by the Gmail toolset.
// *gworkspace.Gmail satisfies this interface implicitly.
type GmailClient interface {
	ReadMessages(ctx context.Context, ownerID string, q gworkspace.MessageQuery) ([]gworkspace.Message, error)
	SendEmail(ctx context.Context, ownerID, to, subject, body string) error
}

// GmailTools returns the Gmail toolset bound to c. It errors if c is nil.
func GmailTools(c GmailClient) ([]adktool.Tool, error) {
	if c == nil {
		return nil, errors.New("adk: GmailTools: client must not be nil")
	}
	type msgsOutput struct {
		Msgs []msgView `json:"messages"`
	}
	type readMsgsArgs struct {
		Query string `json:"query,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	readMsgs, err := functiontool.New(
		functiontool.Config{
			Name: "read_messages",
			Description: `WHEN TO USE:
- The human asks to read email, check their inbox, or find a specific message
- Use a Gmail search query (e.g. "is:unread", "from:alice@example.com", "subject:invoice")

HOW TO USE:
- query: Gmail search query. Leave empty for the most recent messages.
- limit: maximum messages to return. Default to just a few — don't pull hundreds.

WHAT I GET BACK:
- A list of messages with id, from, to, subject, snippet, body (plain text), date.`,
		},
		func(toolCtx adktool.Context, in readMsgsArgs) (msgsOutput, error) {
			q := gworkspace.MessageQuery{
				Query: in.Query,
				Limit: in.Limit,
			}

			msgs, err := c.ReadMessages(toolCtx, toolCtx.UserID(), q)
			if err != nil {
				if errors.Is(err, gworkspace.ErrNotConnected) {
					return msgsOutput{}, errors.New("the human hasn't connected their Gmail account yet — direct them to the OAuth flow before accessing Gmail")
				}
				return msgsOutput{}, err
			}

			views := make([]msgView, len(msgs))
			for i, m := range msgs {
				views[i] = toMsgView(m)
			}
			return msgsOutput{Msgs: views}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	type ack struct {
		OK bool `json:"ok"`
	}
	type sendEmailArgs struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	sendEmail, err := functiontool.New(
		functiontool.Config{
			Name: "send_email",
			Description: `WHEN TO USE:
- The human asks to send an email
- Confirm the recipient, subject, and body before sending if not already explicit

HOW TO USE:
- to: recipient email address (required)
- subject: email subject (required)
- body: plain-text email body (required)

WHAT I GET BACK:
- Confirmation that the send succeeded (ok: true). An error if it failed.`,
		},
		func(toolCtx adktool.Context, in sendEmailArgs) (ack, error) {
			if in.To == "" {
				return ack{}, errors.New("to address is required")
			}
			if in.Subject == "" {
				return ack{}, errors.New("subject is required")
			}
			if in.Body == "" {
				return ack{}, errors.New("body is required")
			}

			err := c.SendEmail(toolCtx, toolCtx.UserID(), in.To, in.Subject, in.Body)
			if err != nil {
				if errors.Is(err, gworkspace.ErrNotConnected) {
					return ack{}, errors.New("the human hasn't connected their Gmail account yet — direct them to the OAuth flow before accessing Gmail")
				}
				return ack{}, err
			}
			return ack{OK: true}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return []adktool.Tool{
		readMsgs,
		sendEmail,
	}, nil
}
