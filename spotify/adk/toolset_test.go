package adk

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	adktool "google.golang.org/adk/tool"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"

	"go.trueardian.com/spotify"
)

func TestToolsNilClient(t *testing.T) {
	if _, err := Tools(nil); err == nil {
		t.Fatal("Tools(nil): want error, got nil")
	}
}

func TestToolsNames(t *testing.T) {
	tools, err := Tools(&stubClient{})
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	got := make(map[string]bool, len(tools))
	for _, tl := range tools {
		got[tl.Name()] = true
	}
	want := []string{
		"search_tracks", "my_playlists", "playlist_tracks",
		"now_playing", "my_devices",
		"play", "transfer_playback", "pause", "skip_next", "skip_previous", "set_volume",
	}
	if len(tools) != len(want) {
		t.Errorf("tool count = %d, want %d", len(tools), len(want))
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestForAgent(t *testing.T) {
	passthrough := errors.New("some other failure")
	tests := []struct {
		name string
		err  error
		want string // substring the translated error must contain
	}{
		{"not connected", spotify.ErrNotConnected, "hasn't connected"},
		{"no device", spotify.ErrNoActiveDevice, "no active Spotify device"},
		{"premium", spotify.ErrPremiumRequired, "Premium"},
		{"rate limited", spotify.ErrRateLimited, "rate-limiting"},
		{"passthrough", passthrough, "some other failure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forAgent(tt.err)
			if got == nil || !strings.Contains(got.Error(), tt.want) {
				t.Errorf("forAgent(%v) = %v, want containing %q", tt.err, got, tt.want)
			}
		})
	}
	if forAgent(nil) != nil {
		t.Error("forAgent(nil): want nil")
	}
}

func TestToPlaybackView(t *testing.T) {
	if got := toPlaybackView(nil); got.Playing || got.Track != nil || got.Device != nil {
		t.Errorf("toPlaybackView(nil) = %+v, want not playing with no track/device", got)
	}

	pb := &spotify.Playback{
		Track:       &spotify.Track{ID: "t1", Name: "Song", Artists: []string{"A"}},
		Device:      spotify.Device{ID: "d1", Name: "Phone"},
		IsPlaying:   true,
		ProgressMs:  4200,
		ContextURI:  "spotify:playlist:p1",
		ContextType: "playlist",
	}
	got := toPlaybackView(pb)
	if !got.Playing || got.ProgressMs != 4200 || got.ContextType != "playlist" {
		t.Errorf("toPlaybackView populated = %+v", got)
	}
	if got.Track == nil || got.Track.Name != "Song" {
		t.Errorf("track not mapped: %+v", got.Track)
	}
	if got.Device == nil || got.Device.Name != "Phone" {
		t.Errorf("device not mapped: %+v", got.Device)
	}
}

func TestSearchTracksFlow(t *testing.T) {
	c := &stubClient{tracks: []spotify.Track{{ID: "t1", Name: "Song One", URI: "spotify:track:t1"}}}

	result, err := runTool(t, c, "user-123", "search_tracks", map[string]any{"query": "song one"})
	if err != nil {
		t.Fatalf("search_tracks: %v", err)
	}
	tracks, ok := result["tracks"].([]any)
	if !ok || len(tracks) != 1 {
		t.Fatalf("tracks = %#v, want one entry", result["tracks"])
	}
	if c.gotUserID != "user-123" {
		t.Errorf("client called for %q, want %q", c.gotUserID, "user-123")
	}
}

func TestSetVolumePremiumError(t *testing.T) {
	c := &stubClient{err: spotify.ErrPremiumRequired}

	_, err := runTool(t, c, "user-1", "set_volume", map[string]any{"percent": 50})
	if err == nil {
		t.Fatal("set_volume: want error, got nil")
	}
	if !strings.Contains(err.Error(), "Premium") {
		t.Errorf("error = %q, want Premium guidance", err)
	}
}

type stubClient struct {
	tracks    []spotify.Track
	err       error
	gotUserID string
}

func (c *stubClient) SearchTracks(_ context.Context, userID, _ string) ([]spotify.Track, error) {
	c.gotUserID = userID
	return c.tracks, c.err
}
func (c *stubClient) UserPlaylists(context.Context, string) ([]spotify.Playlist, error) {
	return nil, c.err
}
func (c *stubClient) PlaylistTracks(context.Context, string, string) ([]spotify.Track, error) {
	return c.tracks, c.err
}
func (c *stubClient) CurrentPlayback(context.Context, string) (*spotify.Playback, error) {
	return nil, c.err
}
func (c *stubClient) Devices(context.Context, string) ([]spotify.Device, error) { return nil, c.err }
func (c *stubClient) Play(context.Context, string, spotify.PlayRequest) error   { return c.err }
func (c *stubClient) TransferPlayback(context.Context, string, string, bool) error {
	return c.err
}
func (c *stubClient) Pause(context.Context, string) error          { return c.err }
func (c *stubClient) Next(context.Context, string) error           { return c.err }
func (c *stubClient) Previous(context.Context, string) error       { return c.err }
func (c *stubClient) SetVolume(context.Context, string, int) error { return c.err }

// runnableTool mirrors the unexported interface the ADK uses to invoke a tool,
// letting the tests call a tool the way the framework does.
type runnableTool interface {
	Run(ctx adktool.Context, args any) (map[string]any, error)
}

func runTool(t *testing.T, c Client, userID, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	tools, err := Tools(c)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	for _, tl := range tools {
		if tl.Name() == name {
			runner, ok := tl.(runnableTool)
			if !ok {
				t.Fatalf("tool %q is not runnable", name)
			}
			return runner.Run(&testContext{Context: context.Background(), userID: userID}, args)
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil, nil
}

// testContext is a minimal adktool.Context for exercising tool handlers,
// reporting a configurable UserID.
type testContext struct {
	context.Context
	userID string
}

func (c *testContext) UserID() string                       { return c.userID }
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
