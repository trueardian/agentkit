package adk

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	adktool "google.golang.org/adk/tool"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"

	"go.naturallyfunny.dev/postera"
)

func TestToolsNilPostarius(t *testing.T) {
	if _, err := Tools(nil); err == nil {
		t.Fatal("Tools(nil): want error, got nil")
	}
}

func TestToolsNames(t *testing.T) {
	tools, err := Tools(newPostarius(&fakeStore{}, &fakeEnqueuer{}))
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	got := make(map[string]bool, len(tools))
	for _, tl := range tools {
		got[tl.Name()] = true
	}
	want := []string{"wake_future_self", "list_upcoming_wakes", "cancel_upcoming_wake"}
	if len(tools) != len(want) {
		t.Errorf("tool count = %d, want %d", len(tools), len(want))
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

// TestTriggerAtDoc_LocalizationNote proves the wake_future_self description gains
// the "no conversion needed" note only when the Postarius localizes from context,
// so the doc an agent reads matches how time is actually handled.
func TestTriggerAtDoc_LocalizationNote(t *testing.T) {
	const note = "no conversion is needed"

	t.Run("localized", func(t *testing.T) {
		p := postera.New(&fakeStore{}, &fakeEnqueuer{}, postera.WithTimezoneFromContext(tzKey{}))
		if !strings.Contains(wakeDescription(t, p), note) {
			t.Error("expected localization note when Postarius localizes from context")
		}
	})
	t.Run("not_localized", func(t *testing.T) {
		p := newPostarius(&fakeStore{}, &fakeEnqueuer{}) // fixed default zone, no context localization
		if strings.Contains(wakeDescription(t, p), note) {
			t.Error("did not expect localization note without context localization")
		}
	})
}

// TestWakeFutureSelfFlow drives wake_future_self end to end: the tool must both
// persist and enqueue the posterum, and hand back a view carrying the id and the
// trigger_at rendered in Postera's TimeLayout.
func TestWakeFutureSelfFlow(t *testing.T) {
	store := &fakeStore{}
	enq := &fakeEnqueuer{}
	p := newPostarius(store, enq)

	const triggerAt = "2026-05-07T22:00:00"
	result := runTool(t, p, "wake_future_self", map[string]any{
		"message":    "Follow up on the Q3 report.",
		"trigger_at": triggerAt,
	})

	if result["message"] != "Follow up on the Q3 report." {
		t.Errorf("message = %v, want the created message", result["message"])
	}
	if id, _ := result["id"].(string); id == "" {
		t.Error("expected a non-empty id in the view")
	}
	if got := result["trigger_at"]; got != triggerAt {
		t.Errorf("trigger_at = %v, want %q (TimeLayout, UTC default zone)", got, triggerAt)
	}
	if len(store.saved) != 1 {
		t.Fatalf("store.Save called %d times, want 1", len(store.saved))
	}
	if len(enq.enqueued) != 1 {
		t.Fatalf("enqueuer.Enqueue called %d times, want 1", len(enq.enqueued))
	}
}

func TestWakeFutureSelf_BadTriggerAt_Errors(t *testing.T) {
	p := newPostarius(&fakeStore{}, &fakeEnqueuer{})
	_, err := runToolErr(t, p, "wake_future_self", map[string]any{
		"message":    "whenever",
		"trigger_at": "not-a-timestamp",
	})
	if err == nil {
		t.Fatal("expected error for unparseable trigger_at, got nil")
	}
}

// TestListUpcomingFlow seeds the store and proves the tool maps every stored
// posterum into a view with its id and localized times.
func TestListUpcomingFlow(t *testing.T) {
	trigger := time.Date(2026, 5, 7, 22, 0, 0, 0, time.UTC)
	store := &fakeStore{list: []postera.Posterum{
		{ID: "pstr_1", Message: "first", TriggerAt: trigger, CreatedAt: trigger},
		{ID: "pstr_2", Message: "second", TriggerAt: trigger, CreatedAt: trigger},
	}}
	p := newPostarius(store, &fakeEnqueuer{})

	result := runTool(t, p, "list_upcoming_wakes", map[string]any{})
	entries, ok := result["entries"].([]any)
	if !ok || len(entries) != 2 {
		t.Fatalf("entries = %#v, want two", result["entries"])
	}
	first, _ := entries[0].(map[string]any)
	if first["id"] != "pstr_1" || first["message"] != "first" {
		t.Errorf("first entry = %#v, want pstr_1/first", first)
	}
	if first["trigger_at"] != "2026-05-07T22:00:00" {
		t.Errorf("trigger_at = %v, want TimeLayout render", first["trigger_at"])
	}
}

// TestCancelFlow seeds a posterum, cancels it through the tool, and proves the
// tool both cancels the enqueue and removes the store record, reporting cancelled.
func TestCancelFlow(t *testing.T) {
	trigger := time.Date(2026, 5, 7, 22, 0, 0, 0, time.UTC)
	store := &fakeStore{
		byID: map[string]postera.Posterum{
			"pstr_1": {ID: "pstr_1", Message: "first", TriggerAt: trigger, CreatedAt: trigger},
		},
	}
	enq := &fakeEnqueuer{}
	p := newPostarius(store, enq)

	result := runTool(t, p, "cancel_upcoming_wake", map[string]any{"id": "pstr_1"})
	if result["id"] != "pstr_1" || result["cancelled"] != true {
		t.Errorf("cancel result = %#v, want pstr_1 cancelled", result)
	}
	if !enq.cancelled["pstr_1"] {
		t.Error("expected enqueuer.Cancel for pstr_1")
	}
	if !store.removed["pstr_1"] {
		t.Error("expected store.Remove for pstr_1")
	}
}

func TestCancel_Unknown_Errors(t *testing.T) {
	p := newPostarius(&fakeStore{byID: map[string]postera.Posterum{}}, &fakeEnqueuer{})
	_, err := runToolErr(t, p, "cancel_upcoming_wake", map[string]any{"id": "pstr_missing"})
	if err == nil {
		t.Fatal("expected error cancelling an unknown id, got nil")
	}
}

// --- helpers ---

// newPostarius builds a Postarius with a fixed UTC default zone, so trigger_at
// parses and renders deterministically without a per-request timezone in context.
func newPostarius(store postera.Store, enq postera.Enqueuer) *postera.Postarius {
	return postera.New(store, enq, postera.WithDefaultTimezone(time.UTC))
}

func wakeDescription(t *testing.T, p *postera.Postarius) string {
	t.Helper()
	tools, err := Tools(p)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	for _, tl := range tools {
		if tl.Name() == "wake_future_self" {
			return tl.Description()
		}
	}
	t.Fatal("wake_future_self tool not found")
	return ""
}

// runnableTool mirrors the unexported interface the ADK uses to invoke a tool,
// letting the tests call a tool the way the framework does.
type runnableTool interface {
	Run(ctx adktool.Context, args any) (map[string]any, error)
}

func runToolErr(t *testing.T, p *postera.Postarius, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	tools, err := Tools(p)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	for _, tl := range tools {
		if tl.Name() == name {
			runner, ok := tl.(runnableTool)
			if !ok {
				t.Fatalf("tool %q is not runnable", name)
			}
			return runner.Run(&testContext{Context: context.Background()}, args)
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil, nil
}

func runTool(t *testing.T, p *postera.Postarius, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := runToolErr(t, p, name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// tzKey is a context key type for WithTimezoneFromContext in the localization test.
type tzKey struct{}

type fakeStore struct {
	saved   []postera.Posterum
	list    []postera.Posterum
	byID    map[string]postera.Posterum
	removed map[string]bool
}

func (s *fakeStore) Save(_ context.Context, p postera.Posterum) error {
	s.saved = append(s.saved, p)
	return nil
}

func (s *fakeStore) Get(_ context.Context, id string) (postera.Posterum, error) {
	if p, ok := s.byID[id]; ok {
		return p, nil
	}
	return postera.Posterum{}, postera.ErrNotFound
}

func (s *fakeStore) Remove(_ context.Context, id string) error {
	if s.removed == nil {
		s.removed = map[string]bool{}
	}
	s.removed[id] = true
	return nil
}

func (s *fakeStore) List(_ context.Context, _ postera.Query) ([]postera.Posterum, error) {
	return s.list, nil
}

type fakeEnqueuer struct {
	enqueued  []postera.Posterum
	cancelled map[string]bool
}

func (e *fakeEnqueuer) Enqueue(_ context.Context, p postera.Posterum) error {
	e.enqueued = append(e.enqueued, p)
	return nil
}

func (e *fakeEnqueuer) Cancel(_ context.Context, id string) error {
	if e.cancelled == nil {
		e.cancelled = map[string]bool{}
	}
	e.cancelled[id] = true
	return nil
}

// testContext is a minimal adktool.Context for exercising tool handlers.
type testContext struct {
	context.Context
}

func (c *testContext) UserID() string                       { return "user-1" }
func (c *testContext) FunctionCallID() string               { return "test-function-call-id" }
func (c *testContext) AgentName() string                    { return "test-agent" }
func (c *testContext) AppName() string                      { return "test-app" }
func (c *testContext) Branch() string                       { return "test-branch" }
func (c *testContext) SessionID() string                    { return "test-session-id" }
func (c *testContext) InvocationID() string                 { return "test-invocation-id" }
func (c *testContext) UserContent() *genai.Content          { return nil }
func (c *testContext) ReadonlyState() session.ReadonlyState { return nil }
func (c *testContext) State() session.State                 { return nil }
func (c *testContext) Artifacts() agent.Artifacts           { return nil }
func (c *testContext) Actions() *session.EventActions       { return &session.EventActions{} }
func (c *testContext) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return nil, nil
}
func (c *testContext) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }
func (c *testContext) RequestConfirmation(string, any) error                { return nil }

var _ adktool.Context = (*testContext)(nil)
