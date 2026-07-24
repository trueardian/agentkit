package adk

import (
	"context"
	"errors"

	adktool "google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"go.naturallyfunny.dev/gworkspace"
)

type contactView struct {
	ResourceName string   `json:"resource_name"`
	Name         string   `json:"name"`
	Emails       []string `json:"emails,omitempty"`
	Phones       []string `json:"phones,omitempty"`
}

func toContactView(c gworkspace.Contact) contactView {
	return contactView{
		ResourceName: c.ResourceName,
		Name:         c.Name,
		Emails:       c.Emails,
		Phones:       c.Phones,
	}
}

// ContactClient is the surface required by the Contacts toolset.
// *gworkspace.Contacts satisfies this interface implicitly.
type ContactClient interface {
	GetContacts(ctx context.Context, ownerID string, q gworkspace.ContactQuery) ([]gworkspace.Contact, error)
	AddContact(ctx context.Context, ownerID string, in gworkspace.ContactInput) (gworkspace.Contact, error)
}

// ContactTools returns the Google Contacts toolset bound to c. It errors if
// c is nil.
func ContactTools(c ContactClient) ([]adktool.Tool, error) {
	if c == nil {
		return nil, errors.New("adk: ContactTools: client must not be nil")
	}
	type contactsOutput struct {
		Contacts []contactView `json:"contacts"`
	}
	type getContactsArgs struct {
		Limit int `json:"limit,omitempty"`
	}
	getContacts, err := functiontool.New(
		functiontool.Config{
			Name: "get_contacts",
			Description: `WHEN TO USE:
- I need to find someone's email or phone number in the human's contacts
- The human names a person but doesn't give their email explicitly
- Before add_event with guests, to confirm the correct guest emails

HOW TO USE:
- limit: maximum contacts to return. Defaults to all contacts (Google's limit applies).
  Set a small limit when only a few are needed, to avoid a large response.

WHAT I GET BACK:
- A list of contacts with resource_name, name, emails, phones.
  Use an email from here to fill in event guests or an email recipient.`,
		},
		func(toolCtx adktool.Context, in getContactsArgs) (contactsOutput, error) {
			q := gworkspace.ContactQuery{
				Limit: in.Limit,
			}
			contacts, err := c.GetContacts(toolCtx, toolCtx.UserID(), q)
			if err != nil {
				if errors.Is(err, gworkspace.ErrNotConnected) {
					return contactsOutput{}, errors.New("the human hasn't connected their Google account yet — direct them to the OAuth flow before accessing Contacts")
				}
				return contactsOutput{}, err
			}
			views := make([]contactView, len(contacts))
			for i, ct := range contacts {
				views[i] = toContactView(ct)
			}
			return contactsOutput{Contacts: views}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	type addContactArgs struct {
		Name   string   `json:"name"`
		Emails []string `json:"emails,omitempty"`
		Phones []string `json:"phones,omitempty"`
	}
	addContact, err := functiontool.New(
		functiontool.Config{
			Name: "add_contact",
			Description: `WHEN TO USE:
- The human asks to save a new contact
- Confirm the name, email, and phone before saving

HOW TO USE:
- name: the person's full name (required)
- emails: list of email addresses (optional but recommended)
- phones: list of phone numbers (optional)

WHAT I GET BACK:
- The created contact with its resource_name.`,
		},
		func(toolCtx adktool.Context, in addContactArgs) (contactView, error) {
			if in.Name == "" {
				return contactView{}, errors.New("name is required")
			}

			input := gworkspace.ContactInput{
				Name:   in.Name,
				Emails: in.Emails,
				Phones: in.Phones,
			}

			created, err := c.AddContact(toolCtx, toolCtx.UserID(), input)
			if err != nil {
				if errors.Is(err, gworkspace.ErrNotConnected) {
					return contactView{}, errors.New("the human hasn't connected their Google account yet — direct them to the OAuth flow before accessing Contacts")
				}
				return contactView{}, err
			}
			return toContactView(created), nil
		},
	)
	if err != nil {
		return nil, err
	}
	return []adktool.Tool{
		getContacts,
		addContact,
	}, nil
}
