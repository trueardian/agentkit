# agentkit

[![Go Reference](https://pkg.go.dev/badge/go.trueardian.com/agentkit.svg)](https://pkg.go.dev/go.trueardian.com/agentkit)
![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A Go library that plugs your clients into an agent framework.

## Install

```sh
go get go.trueardian.com/agentkit
```

## The packages

| Package          | What it is                                                         | Provides                                | Depends on                     |
| ---------------- | ------------------------------------------------------------------ | --------------------------------------- | ------------------------------ |
| `zep/adk`        | Zep implementation of ADK's session and memory services.           | `session.Service`, `memory.Service`     | `github.com/getzep/zep-go/v3`  |
| `spotify/adk`    | Search and playback control, as 11 tools.                          | `[]tool.Tool`                           | `go.trueardian.com/spotify`    |
| `tuya/adk`       | The human's smart home, read and controlled, as 4 tools.           | `[]tool.Tool`                           | `go.trueardian.com/tuya`       |
| `gworkspace/adk` | Gmail, Calendar and Contacts, 2 tools each.                        | `[]tool.Tool`                           | `go.trueardian.com/gworkspace` |
| `postera/adk`    | Lets the agent wake its future self, as 3 tools.                   | `[]tool.Tool`                           | `go.trueardian.com/postera`    |

```
zep/adk        ──▶ zep-go     ─┐
spotify/adk    ──▶ spotify     │
tuya/adk       ──▶ tuya        ├─▶ and all of them ──▶ adk
gworkspace/adk ──▶ gworkspace  │
postera/adk    ──▶ postera    ─┘
```

The layout is `<integration>/<framework>`. The clients don't know any agent framework exists, so
they stay usable from an HTTP handler, a cron job or a CLI. The binding is the only place that knows
both sides. Today the only framework is [Google's Agent Development Kit](https://google.github.io/adk-docs/),
so every leaf is `adk`, and every package is named `adk` too. Alias them on import.

Only import what you need. Only the bindings you import end up in your binary. Below are the
explanations and guides.

## `package zep/adk`

So you are building an agent on ADK, and you want [Zep](https://www.getzep.com/) to hold the
conversations and remember the human across all of them? I got you.

### Sessions

Every ADK session is one Zep thread, and the session ID is the thread ID. Hand your Zep client to
`NewSessionService`:

```go
import zepadk "go.trueardian.com/agentkit/zep/adk"

svc := zepadk.NewSessionService(zepClient,
    zepadk.WithMessageHistoryLength(10),
    zepadk.WithInstruction("temp:session-instruction"),
    zepadk.WithTimeHarness(zepadk.StaticTZ("Asia/Jakarta")),
)

rnr, err := runner.New(runner.Config{
    // ...
    SessionService:    svc,
    AutoCreateSession: true,
})
```

`Create` makes the Zep user first if it doesn't exist yet, then the thread. `Get` loads the last
`WithMessageHistoryLength` messages as history. Leave it at zero and `Get` only checks the thread is
there and loads nothing. `AppendEvent` writes every turn with text in it back to the thread.

Every history message comes back with its speaker in front, like `[Ardian] hi`, and models love to
copy that prefix into their own reply. So `WithInstruction(key)` writes a block into session state
under that key, telling the model what the prefix is and to never write one. Put the placeholder in
your agent instruction and the ADK runner fills it in:

```
~ your instruction ~
{temp:session-instruction?}
```

The `?` keeps the turn alive when the block is empty. Without `WithInstruction`, nothing is written.

### Time

Your agent has no idea what time it is, or that the last message was three days ago.
`WithTimeHarness` fixes both. The prefix becomes `[2026-10-22 09:15 Ardian]`, and the instruction
block gains the current local time and how long it's been since the previous message, so the model
never has to look it up or do timezone math.

The zone comes from one of two resolvers:

```go
zepadk.WithTimeHarness(zepadk.StaticTZ("Asia/Jakarta"))

zepadk.WithTimeHarness(zepadk.TZFromContext())
ctx = zepadk.WithTimezone(ctx, "Asia/Jakarta") // per request, in your middleware
```

`StaticTZ` with an invalid name panics, because that string is right there in your source and it's a
bug to fix now. A bad or missing zone from context is data, so that one returns an error from `Get`.
`WithTimeHarness(nil)` is time-aware without converting anything: timestamps stay in the zone Zep
gave them and the current time is UTC.

Do note the harness is strict. A history message without a parseable `CreatedAt` makes `Get` fail,
instead of quietly showing the model a message with no time on it.

### Speakers

By default a human's turn is saved under the session's user ID. Want a real name, or a different
Zep role? Use a speaker resolver:

```go
zepadk.WithSpeakerResolver(zepadk.StaticSpeaker(zepadk.Speaker{Name: "Ardian"}))

zepadk.WithSpeakerResolver(zepadk.SpeakerFromContext())
ctx = zepadk.WithSpeaker(ctx, zepadk.Speaker{Name: "Ardian"}) // per request, in your middleware
```

An empty `Role` keeps the role ADK gave the turn.

`SpeakerFromContext` makes `AppendEvent` fail when the speaker is missing or has no name, so a
forgotten middleware doesn't silently mislabel the turn.

### Ownership

A thread has to belong to the user asking for it. If it doesn't, `Get` returns
`zepadk.ErrSessionOwnerMismatch`. Map it to a 403. If Zep doesn't report an owner at all, that
counts as a mismatch too, because ownership it can't confirm is ownership it denies.

`Create` runs the same check. With `AutoCreateSession`, the runner calls `Create` after *any* `Get`
error, the rejected cross-user one included, and creating someone else's thread on top of theirs is
not a thing that should be left to Zep to decide. An empty user ID is not checked at all, so your own
system tooling can still reach every thread.

Function calls, function responses and empty model turns carry no text, so they are never written
to Zep. They are still kept in memory, so the ADK flow loop sees them. A turn that is nothing but
invisible characters like `U+200B` counts as empty too.

### Memory

```go
mem := zepadk.NewMemoryService(zepClient)
```

`SearchMemory` searches the user's Zep knowledge graph, independent of any thread, and hands back
Zep's context block as a single memory. `AddSessionToMemory` does nothing, because every turn is
already in a Zep thread and Zep builds the graph from those by itself. Register
`preloadmemorytool.New()` on your agent and the memory gets into every request.

## `package spotify/adk`

So you want your agent to put music on? Hand it your `*spotify.Client`:

```go
import spotifyadk "go.trueardian.com/agentkit/spotify/adk"

tools, err := spotifyadk.Tools(spotifyClient)
```

That hands your agent `search_tracks`, `my_playlists`, `playlist_tracks`, `now_playing`,
`my_devices`, `play`, `transfer_playback`, `pause`, `skip_next`, `skip_previous` and `set_volume`.
The prompting is already written, including the part where the model finds an idle device and plays
on it, instead of giving up the moment nothing is active.

## `package tuya/adk`

So you want your agent to turn the lights off? Hand it your `*appaccount.Service`:

```go
import tuyaadk "go.trueardian.com/agentkit/tuya/adk"

tools, err := tuyaadk.Tools(app)
```

That hands your agent `get_account`, `list_devices`, `device_status` and `send_commands`.
`list_devices` comes with the human's own names for every channel of a multi-gang switch, so "the
kitchen light" finds `switch_2`. `Tools` takes a `tuyaadk.Client`, the four methods it actually
calls, so a fake is all you need in a test.

## `package gworkspace/adk`

So you want your agent to read mail, check the calendar, or find someone's email address? One call
per service, each taking its own `*gworkspace` client:

```go
import gworkspaceadk "go.trueardian.com/agentkit/gworkspace/adk"

gmailTools, err := gworkspaceadk.GmailTools(gmail)          // read_messages, send_email
calendarTools, err := gworkspaceadk.CalendarTools(calendar) // get_events, add_event
contactTools, err := gworkspaceadk.ContactTools(contacts)   // get_contacts, add_contact
```

Same as Tuya, each takes a small interface of exactly the methods it calls.

## `package postera/adk`

So you want your agent to come back to things on its own? Hand it your `*postera.Postarius`:

```go
import posteraadk "go.trueardian.com/agentkit/postera/adk"

tools, err := posteraadk.Tools(postarius)
```

That hands your agent `wake_future_self`, `list_upcoming_wakes` and `cancel_upcoming_wake`. If the
`Postarius` resolves the timezone from context, the tool tells the model no conversion is needed,
and every time comes back in the zone the agent wrote it in.

## Every tool binding

They all work the same way.

The ADK user ID is the owner. Every tool passes `toolCtx.UserID()` to the client as the owner, so
run the agent under the same ID your client keeps the human's tokens or Tuya link under.

They fail at wiring time. A nil client returns an error from `Tools`, at startup next to the line
that caused it, instead of a nil-panic in the middle of a conversation.

The errors are written for the model. `spotify.ErrNoActiveDevice` means nothing to a model deciding
what to do next, so the errors the binding knows (not connected, not linked, no active device,
Premium required, rate limited) are rewritten into what to do about them. Anything else passes
through untouched, so a real bug never gets smoothed over into a friendly lie.

The JSON is its own contract. No client type is handed to the model directly. Each binding has its
own view types, so a client refactor can't silently reshape what the model sees.

## What this library doesn't do

- **Authentication or tenant isolation.** The ownership check in `zep/adk` is a backstop, not an
  auth layer. Who the user is gets decided above this library, at your HTTP or gRPC layer.
- **List or delete Zep sessions.** `SessionService.List` and `Delete` do nothing, and only exist to
  satisfy the interface. Threads are managed in Zep.
- **Model providers.** ADK ships its own, and a custom `model.LLM` is a small type of its own. This
  library is about tools, sessions and memory.
- **Any framework other than ADK.** Not yet. The layout already leaves room: a `spotify/langchaingo`
  would sit right next to `spotify/adk` without moving anything. Pull requests are welcome, though
  I'm fairly strict about how code is written here (see [CLAUDE.md](CLAUDE.md)), so expect me to
  refactor yours before merging.

## License

[MIT](LICENSE) © 2026 Ardian
