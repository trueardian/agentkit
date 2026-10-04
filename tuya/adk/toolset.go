// Package adk exposes a Tuya Cloud client as a set of ADK tools, giving an
// agent its own hands on the human's smart home: seeing their devices, reading
// what state each is in, and switching them on or off.
package adk

import (
	"context"
	"errors"

	adktool "google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"go.trueardian.com/tuya"
	"go.trueardian.com/tuya/appaccount"
)

// Client is the surface the toolset drives. appaccount.Service satisfies it.
// Get and Devices take the human's owner id (toolCtx.UserID()) and resolve it to
// their Tuya UID; DeviceStatus and SendCommands address the device directly.
type Client interface {
	Get(ctx context.Context, owner string) (appaccount.Account, error)
	Devices(ctx context.Context, owner string, opts ...tuya.DeviceOption) ([]tuya.UserDevice, error)
	DeviceStatus(ctx context.Context, deviceID string) ([]tuya.DataPoint, error)
	SendCommands(ctx context.Context, deviceID string, commands []tuya.DataPoint) error
}

var _ Client = (*appaccount.Service)(nil)

// dataPointView is one Tuya data point (DP): a capability code and its value.
// It is how a device reports state and how it's told to change — e.g.
// {"code": "switch_1", "value": true}.
type dataPointView struct {
	Code  string `json:"code"`
	Value any    `json:"value"`
}

// channelView names one switch/outlet of a multi-gang device, tying its DP code
// (e.g. "switch_1") to the human's label (e.g. "Kitchen light").
type channelView struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
}

// deviceView is a device with its current state and, for multi-gang
// switches/outlets, the human's per-channel names.
type deviceView struct {
	ID       string          `json:"id"`
	Category string          `json:"category"`
	Name     string          `json:"name"`
	Status   []dataPointView `json:"status"`
	Channels []channelView   `json:"channels"`
}

// accountView reports the human's linked Tuya account.
type accountView struct {
	Owner   string `json:"owner"`
	TuyaUID string `json:"tuya_uid"`
}

// noArgs is shared by the tools that take no input.
type noArgs struct{}

type deviceIDArgs struct {
	DeviceID string `json:"device_id"`
}

type sendCommandsArgs struct {
	DeviceID string          `json:"device_id"`
	Commands []dataPointView `json:"commands"`
}

type devicesOutput struct {
	Devices []deviceView `json:"devices"`
}

type statusOutput struct {
	Status []dataPointView `json:"status"`
}

// ack confirms a command landed. A returned error means it didn't; ack{OK: true}
// means it did.
type ack struct {
	OK bool `json:"ok"`
}

// Tools returns the Tuya toolset bound to c. It errors if c is nil so the
// failure surfaces at wiring time, not on the first tool call.
func Tools(c Client) ([]adktool.Tool, error) {
	if c == nil {
		return nil, errors.New("adk: Tools: client must not be nil")
	}

	getAccount, err := functiontool.New(
		functiontool.Config{
			Name: "get_account",
			Description: `Check which Tuya account is linked to the human I'm helping.

WHEN TO USE:
- Before anything else, to confirm the human has linked a Tuya account at all
- The human asks whose smart home I'm connected to

WHAT I GET BACK:
- The linked Tuya account. If nothing is linked, the human needs to link one
  before I can see or control any device.`,
		},
		func(toolCtx adktool.Context, _ noArgs) (accountView, error) {
			acc, err := c.Get(toolCtx, toolCtx.UserID())
			if err != nil {
				return accountView{}, forAgent(err)
			}
			return accountView{Owner: acc.Owner, TuyaUID: acc.TuyaUID}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	listDevices, err := functiontool.New(
		functiontool.Config{
			Name: "list_devices",
			Description: `See every device in the human's smart home — name, category, current state, and the id I need to do anything with it.

WHEN TO USE:
- The human asks what devices they have, or what's on/off
- I need a device's id before reading its status or sending it a command
- For multi-gang switches I want each channel's human-given name

WHAT I GET BACK:
- Each device carries its id, category, status (data points), and — for
  multi-gang switches/outlets — channels linking each switch to its label.
  Always use the exact id from here; a wrong id is fatal.`,
		},
		func(toolCtx adktool.Context, _ noArgs) (devicesOutput, error) {
			devices, err := c.Devices(toolCtx, toolCtx.UserID(), tuya.WithChannelNames())
			if err != nil {
				return devicesOutput{}, forAgent(err)
			}
			return devicesOutput{Devices: toDeviceViews(devices)}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	deviceStatus, err := functiontool.New(
		functiontool.Config{
			Name: "device_status",
			Description: `Read the current state of one device — its data points right now.

WHEN TO USE:
- I want a single device's up-to-date state without listing everything
- Before flipping something, to know what state it's already in

HOW TO USE:
- device_id: from list_devices. Use the exact id.

WHAT I GET BACK:
- The device's data points (code + value), e.g. switch_1=true, bright_value=600.`,
		},
		func(toolCtx adktool.Context, in deviceIDArgs) (statusOutput, error) {
			status, err := c.DeviceStatus(toolCtx, in.DeviceID)
			if err != nil {
				return statusOutput{}, forAgent(err)
			}
			return statusOutput{Status: toDataPointViews(status)}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	sendCommands, err := functiontool.New(
		functiontool.Config{
			Name: "send_commands",
			Description: `Drive a device — switch it on or off, set brightness, change color, whatever its data points allow.

WHEN TO USE:
- The human asks to turn something on/off or change a setting
- I've found the device and the data point I want to change

HOW TO USE:
- device_id: from list_devices. Use the exact id — a wrong id is fatal.
- commands: a list of data points to set, each a code and value, e.g.
  [{"code": "switch_1", "value": true}] to switch on, or
  [{"code": "bright_value", "value": 600}] to set brightness. Read a device's
  status or channels first if I'm unsure which code does what.`,
		},
		func(toolCtx adktool.Context, in sendCommandsArgs) (ack, error) {
			if err := c.SendCommands(toolCtx, in.DeviceID, toDataPoints(in.Commands)); err != nil {
				return ack{}, forAgent(err)
			}
			return ack{OK: true}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	return []adktool.Tool{
		getAccount,
		listDevices,
		deviceStatus,
		sendCommands,
	}, nil
}

// forAgent rewrites the client's sentinel errors into guidance the agent can
// act on, leaving anything unrecognized untouched. The model reading the error
// should know what to do next.
func forAgent(err error) error {
	switch {
	case errors.Is(err, appaccount.ErrNotLinked):
		return errors.New("the human hasn't linked their Tuya account yet — they need to link it before I can see or control any device")
	default:
		return err
	}
}

func toDataPointView(d tuya.DataPoint) dataPointView {
	return dataPointView{Code: d.Code, Value: d.Value}
}

func toDataPointViews(dps []tuya.DataPoint) []dataPointView {
	views := make([]dataPointView, len(dps))
	for i, d := range dps {
		views[i] = toDataPointView(d)
	}
	return views
}

func toDataPoints(views []dataPointView) []tuya.DataPoint {
	dps := make([]tuya.DataPoint, len(views))
	for i, v := range views {
		dps[i] = tuya.DataPoint{Code: v.Code, Value: v.Value}
	}
	return dps
}

func toChannelViews(channels []tuya.Channel) []channelView {
	views := make([]channelView, len(channels))
	for i, ch := range channels {
		views[i] = channelView{Identifier: ch.Identifier, Name: ch.Name}
	}
	return views
}

func toDeviceViews(devices []tuya.UserDevice) []deviceView {
	views := make([]deviceView, len(devices))
	for i, d := range devices {
		views[i] = deviceView{
			ID:       d.ID,
			Category: d.Category,
			Name:     d.Name,
			Status:   toDataPointViews(d.Status),
			Channels: toChannelViews(d.Channels),
		}
	}
	return views
}
