package adk

import (
	"context"
	"errors"
	"testing"

	"go.trueardian.com/tuya"
	"go.trueardian.com/tuya/appaccount"
)

// stubClient implements Client for tests that only need tool registration to succeed.
type stubClient struct{}

func (stubClient) Get(context.Context, string) (appaccount.Account, error) {
	return appaccount.Account{}, nil
}
func (stubClient) Devices(context.Context, string, ...tuya.DeviceOption) ([]tuya.UserDevice, error) {
	return nil, nil
}
func (stubClient) DeviceStatus(context.Context, string) ([]tuya.DataPoint, error) { return nil, nil }
func (stubClient) SendCommands(context.Context, string, []tuya.DataPoint) error   { return nil }

func TestToolsNilClient(t *testing.T) {
	if _, err := Tools(nil); err == nil {
		t.Fatal("Tools(nil): want error, got nil")
	}
}

func TestToolsNames(t *testing.T) {
	tools, err := Tools(stubClient{})
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	got := make(map[string]bool, len(tools))
	for _, tl := range tools {
		got[tl.Name()] = true
	}
	want := []string{"get_account", "list_devices", "device_status", "send_commands"}
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
		{"not linked", appaccount.ErrNotLinked, "hasn't linked"},
		{"passthrough", passthrough, "some other failure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forAgent(tt.err)
			if got == nil || !contains(got.Error(), tt.want) {
				t.Errorf("forAgent(%v) = %v, want substring %q", tt.err, got, tt.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
