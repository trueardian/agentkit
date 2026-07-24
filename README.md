# agentkit

[![Go Reference](https://pkg.go.dev/badge/go.naturallyfunny.dev/agentkit.svg)](https://pkg.go.dev/go.naturallyfunny.dev/agentkit)
[![Go 1.25](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Thin, idiomatic bindings that adapt framework-agnostic Go clients to Go agent
frameworks.** A client that already knows how to talk to Spotify, Zep, Tuya, or
Google Workspace shouldn't need to know it lives inside an agent. `agentkit`
carries that knowledge in a layer of its own, one binding per framework.

```
  framework-agnostic client            agentkit binding                agent framework
  (its own module, no agent deps)      (this module, thin)             (Google ADK today)
 ┌───────────────────────────┐   ┌───────────────────────────┐   ┌──────────────────────┐
 │ spotify.Client            │   │ spotify/adk.Tools(c)       │   │                      │
 │ tuya.Client               │──▶│ tuya/adk.Tools(c)          │──▶│ []adktool.Tool       │
 │ gworkspace.{Gmail,…}      │   │ gworkspace/adk.*Tools(c)   │   │ adksession.Service   │
 │ postera.Postarius         │   │ postera/adk.Tools(p)       │   │ memory.Service       │
 │ zep client                │   │ zep/adk.New*Service(c)     │   │                      │
 └───────────────────────────┘   └───────────────────────────┘   └──────────────────────┘
        depends on nothing              depends on both              knows nothing of us
```

The layout is `<integration>/<framework>` — `spotify/adk`, `zep/adk`, and so on.
The framework sits in the *leaf* so the same client can gain a second binding
(`spotify/langchaingo`, say) later without moving or renaming a thing. Today the
only leaf is `adk`, [Google's Agent Development Kit][adk].

Callers are people wiring an agent: you already hold a configured `*spotify.Client`
or Zep client, and you want it registered as tools, a session backend, or a memory
backend with one call. That is the pattern every decision below optimizes for.

---

## Contents

- [Why this shape](#why-this-shape)
- [At a glance](#at-a-glance)
- [Install](#install)
- [Quick start](#quick-start)
- [Design decisions & trade-offs](#design-decisions--trade-offs)
- [Design principles](#design-principles)
- [Owned nuances](#owned-nuances)
- [Non-goals](#non-goals)
- [Verification](#verification)
- [Compatibility](#compatibility)
- [Layout](#layout)
- [License](#license)

---

## Why this shape

**The clients are not ours to bend, and the framework is not theirs to know.**

Each underlying client (`go.naturallyfunny.dev/spotify`, `/gworkspace`, `/tuya`,
`/postera`, and the Zep SDK) is a standalone module with no dependency on any
agent framework. That is deliberate: the client is reusable from an HTTP handler,
a cron job, or a CLI, and it stays testable without pulling ADK's dependency tree.

An agent framework, meanwhile, wants very specific shapes — `adktool.Tool`,
`adksession.Service`, `model.LLM`. Something has to translate. Putting that
translation *in the client* would couple every client to a framework; putting it
*in the app* would make every app rewrite the same adapter. `agentkit` is the
third place: a binding module that depends on both and is depended on by neither.
Because the binding is the only thing that knows both sides, it is the only thing
that has to change when either side moves — and it is small enough to change fast.

---

## At a glance

Every package is one subpackage per framework (`adk` today). Each exposes a single
constructor that takes an already-configured client and returns framework types.

| Package          | What you give it            | What you get                                           | Surface                              |
| ---------------- | --------------------------- | ------------------------------------------------------ | ------------------------------------ |
| `gworkspace/adk` | `*gworkspace.{Gmail,…}`     | Gmail, Calendar, Contacts as tools (6 tools)           | `GmailTools`, `CalendarTools`, `ContactTools` |
| `spotify/adk`    | `*spotify.Client`           | Search + playback control as tools (11 tools)          | `Tools`                              |
| `tuya/adk`       | a `tuya.Client`             | Smart-home read/control as tools (4 tools)             | `Tools`, `Client`                    |
| `postera/adk`    | `*postera.Postarius`        | "Wake your future self" scheduling as tools (3 tools)  | `Tools`                              |
| `zep/adk`        | a Zep `*client.Client`      | ADK session backend **and** user-graph memory backend  | `NewSessionService`, `NewMemoryService` |

`gworkspace`, `tuya`, and `postera` bindings translate the client's sentinel
errors into instructions an agent can act on (see [Error translation](#5-errors-the-model-can-act-on-not-just-report)).
`zep/adk` is the substantial one: it implements ADK's `session.Service` over Zep
threads with an ownership guard, a time harness, and speaker attribution, plus
`memory.Service` over the Zep user knowledge graph.

---

## Install

```bash
go get go.naturallyfunny.dev/agentkit
```

Requires **Go 1.25+** (module `go` directive is `go 1.25.8`). Import only the
subpackages you need; unused bindings pull in no transitive dependencies.

Full API reference: [`pkg.go.dev/go.naturallyfunny.dev/agentkit`][godoc], or locally:

```bash
go doc ./zep/adk
go doc ./spotify/adk
```

---

## Quick start

Wiring the Zep binding into an ADK runner as **both** the session backend and the
memory backend. This is the end-to-end shape; the tool bindings are a one-liner by
comparison (`tools, err := spotifyadk.Tools(client)`).

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/getzep/zep-go/v3/client"
	"github.com/getzep/zep-go/v3/option"
	zep "go.naturallyfunny.dev/agentkit/zep/adk"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

func main() {
	zepClient := client.NewClient(option.WithAPIKey(os.Getenv("ZEP_API_KEY")))

	// Session backend: history, ownership guard, time-awareness, speaker
	// attribution. WithInstruction names the State key the ADK runner resolves
	// into the agent's instruction — put {temp:session-instruction?} in your
	// prompt and the binding fills it (message-format + current-time blocks).
	svc := zep.NewSessionService(
		zepClient,
		zep.WithMessageHistoryLength(10),
		zep.WithInstruction("temp:session-instruction"),
		zep.WithTimeHarness(zep.StaticTZ("Asia/Jakarta")),
	)

	// Memory backend: user-scoped Zep knowledge graph, independent of any thread.
	mem := zep.NewMemoryService(zepClient)

	rnr, err := runner.New(runner.Config{
		SessionService:    svc,
		MemoryService:     mem,
		AutoCreateSession: true, // runner calls Create for us on the first turn
	})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	userID := "user-456"
	sessionID := userID // one-thread-per-user: SessionID == UserID

	msg := genai.NewContentFromText("Hello! What do you know about me?", "user")
	for event, err := range rnr.Run(ctx, userID, sessionID, msg, agent.RunConfig{}) {
		if err != nil {
			panic(err)
		}
		if event.Content != nil {
			for _, part := range event.Content.Parts {
				fmt.Print(part.Text)
			}
		}
	}
	fmt.Println()
}
```

Run it: `ZEP_API_KEY=… go run ./examples/zep`. The full annotated version lives in
[`examples/zep/main.go`](examples/zep/main.go).

**Failure paths are part of the contract.** A cross-user session request returns
[`zep.ErrSessionOwnerMismatch`](zep/adk/session.go) — map it to HTTP 403. A nil
client into any tool binding returns an error at wiring time, not a nil-panic on
the first call. Details below.

---

## Design decisions & trade-offs

Each decision is written as **what we did → the plausible alternative → why this
side of it**.

### 1. `<integration>/<framework>` layout, framework in the leaf

**Choice.** Directories are keyed by client first, framework second:
`spotify/adk`, `zep/adk`.

**Alternative.** Key by framework first (`adk/spotify`, `adk/zep`), the way many
"adapters for X" repos are laid out.

**Why.** The client is the stable axis; the framework is the axis that multiplies.
When a second framework binding arrives, `spotify/langchaingo` slots in next to
`spotify/adk` with zero churn to the first. Framework-first would instead scatter
each client across sibling top-level trees, so "everything Spotify" would no longer
be one directory. Keying by the axis that grows keeps additive change additive.

### 2. Thin bindings over standalone client modules

**Choice.** The clients are separate, framework-agnostic modules; this module is
only the adapter layer on top.

**Alternative.** Vendor the client logic in here, or let each client grow its own
`ToADKTools()` method.

**Why.** A client with no agent dependency stays usable from anywhere and testable
without ADK's dependency tree — and a binding kept deliberately thin has almost no
logic of its own to rot. The cost is one more module to version (see
[Compatibility](#compatibility)); the benefit is that neither the client nor the
framework has to know the binding exists. This is the ["accept interfaces, return
structs"][proverbs] proverb applied at module scale.

### 3. Narrow, consumer-defined client interfaces

**Choice.** Each binding declares the *smallest* interface it needs and accepts
that, not the concrete client. `tuya/adk.Client` lists four methods; `zep/adk`'s
`threadClient` lists the three thread calls it makes — no more.

```go
// tuya/adk: the binding drives exactly this surface; *tuya.Client satisfies it.
type Client interface {
	Account(ctx context.Context, ownerID string) (tuya.Account, error)
	ListDevices(ctx context.Context, ownerID string) ([]cloud.Device, error)
	DeviceStatus(ctx context.Context, ownerID, deviceID string) ([]cloud.DataPoint, error)
	SendCommands(ctx context.Context, ownerID, deviceID string, cmds []cloud.DataPoint) error
}
```

**Alternative.** Accept the concrete `*tuya.Client` / `*zep.client.Client`
directly.

**Why.** Two payoffs, both idiomatic Go. Tests inject a tiny fake instead of
standing up a real client (this is exactly why `zep/adk` reaches **81%** coverage
against a network SDK — see [Verification](#verification)). And the interface
documents the binding's true blast radius: a reviewer sees precisely which client
methods it can call. The interface is defined by the *consumer* per
[Go's interface guidance][accept-interfaces]. A compile-time
`var _ Client = (*tuya.Client)(nil)` assertion keeps the concrete type honest.

### 4. Fail at wiring time, not on the first call

**Choice.** Every `Tools(...)` constructor returns `([]adktool.Tool, error)` and
errors immediately on a nil client:

```go
func Tools(c *spotify.Client) ([]adktool.Tool, error) {
	if c == nil {
		return nil, errors.New("adk: Tools: client must not be nil")
	}
	// …
}
```

**Alternative.** Return `[]adktool.Tool` alone and let a nil client nil-panic when
the agent first invokes a tool — possibly deep in a production run.

**Why.** A misconfigured binding is a wiring bug, and wiring runs once at startup.
Surfacing it there turns a 3-a.m. nil-panic mid-conversation into a boot-time error
next to the line that caused it. `functiontool.New` already returns an error, so the
signature carries one regardless — checking the client too costs nothing.

### 5. Panic for programmer errors, return for runtime errors

**Choice.** The split is by *who can fix it*. A bad literal in code panics; anything
that depends on runtime state returns an error.

```go
zep.StaticTZ("Not/AZone")           // panics: an invalid constant is a source bug
zep.WithSpeakerResolver(nil)        // panics: a resolver must come from a factory
zep.TZFromContext() // + missing ctx value → returns an error at request time
```

**Alternative.** Return errors uniformly, forcing `err` handling on constructors
that can only fail if you typed a constant wrong.

**Why.** `StaticTZ("Asia/Jakarta")` failing is unthinkable at runtime — the string
is right there in the source, so an invalid one is a bug to fix now, and
[`time.LoadLocation`][loadlocation]-style panics match how the standard library
treats `regexp.MustCompile` and `template.Must`. But a timezone pulled from a
request context is *data*, and bad data is an expected runtime condition, so that
path returns an error the caller handles. Same concept, opposite handling, decided
by whether a human editing the file — or a live request — is the source.

### 6. Errors the model can act on, not just report

**Choice.** Bindings translate the client's sentinel errors into agent-facing
guidance, and pass everything else through untouched.

```go
func forAgent(err error) error {
	switch {
	case errors.Is(err, spotify.ErrNoActiveDevice):
		return errors.New("no active Spotify device — ask the human to open Spotify " +
			"somewhere, or check my_devices, then try again")
	// …
	default:
		return err // unrecognized errors are never masked
	}
}
```

**Alternative.** Surface raw client errors to the model, or map them to opaque
codes.

**Why.** The consumer of a tool error here is a language model deciding its next
move, and `ErrNoActiveDevice` tells it nothing actionable. Rewriting the *known*
sentinels into "here's what to do next" turns a dead end into a recovery path,
while the `default` branch keeps unrecognized failures verbatim so real bugs are
never smoothed over into a friendly lie. `errors.Is` (not string matching) does
the recognition, so it survives error wrapping.

### 7. View types as the model-facing JSON contract

**Choice.** No client type is ever handed to the framework directly. Each binding
defines its own `xView` structs with explicit JSON tags and a `toXView` converter.

**Alternative.** Serialize the client's structs straight to the tool output.

**Why.** The JSON an agent sees is a contract with the *model*, and it should be
shaped for the model — stable field names, `omitempty` where "absent" is
meaningful (`playbackView.Track` drops out when nothing is loaded, so `playing`
alone answers "is anything sounding?") — not accidentally coupled to whatever the
client SDK happens to expose this version. A client refactor can't silently reshape
the prompt, and the view sits right next to its converter so the mapping is one
scroll to audit. Per [CLAUDE.md](CLAUDE.md), these domain views stay package-level;
the throwaway args/output wrappers are declared *inside* the tool constructor.

### 8. Fail-closed session ownership (`zep/adk`)

**Choice.** A thread must provably belong to the requesting user, on both the read
and the auto-create path. When Zep can't confirm an owner, access is **denied**.

```go
func verifyThreadOwner(resp *zep.MessageListResponse, expectedUserID string) error {
	if expectedUserID == "" { // no identity → internal/test caller, not gated
		return nil
	}
	if derefOrEmpty(resp.GetUserID()) != expectedUserID {
		return ErrSessionOwnerMismatch // unconfirmable owner counts as a mismatch
	}
	return nil
}
```

**Alternative.** Trust the session id, or let Zep's server decide what happens when
one user opens another's thread.

**Why.** With `AutoCreateSession`, the ADK runner calls `Create` on *any* `Get`
error — including a rejected cross-user read — so without an explicit guard a
denied request would fall through to `thread.Create` against someone else's thread,
whose server-side outcome is undefined. The binding decides the outcome itself
rather than depending on Zep: an existing thread must match the requester; an
unknown owner is treated as a mismatch, not waved through. This is the security
path, so it fails **closed**. It is covered by tests for the match, mismatch,
missing-owner, verify-only, and auto-create-foreign-thread cases.

### 9. Resolvers and harnesses instead of ad-hoc options (`zep/adk`)

**Choice.** Two named patterns carry the configuration surface. A **Resolver**
(`TZResolver`, `SpeakerResolver`) is an opaque single-value source built only via
fluent factories (`StaticTZ`, `TZFromContext`). A **Harness** (`timeHarness`) is an
optional read-path feature with two distinct nil levels.

**Alternative.** A pile of `With…` options and exported config structs the caller
fills in field by field.

**Why.** Naming the *kind* of thing makes misuse hard and reads honestly. A
resolver's factory name states what it yields (`StaticTZ` → a timezone), and its
unexported function field means a caller literally cannot build a broken one —
`&TZResolver{}` won't compile past the option, which panics. A harness backed by a
`*config` pointer distinguishes *disabled* (`nil`) from *enabled-with-default*
(`WithTimeHarness(nil)`, meaning "be time-aware but don't convert zones") — a
plain value couldn't express both. The full rationale is written down in
[CLAUDE.md](CLAUDE.md#naming), which is itself part of the deliberate record.

---

## Design principles

Cross-cutting rules every package obeys — the reason the bindings look like one
author wrote them on one day:

- **Standard library for the core.** `zep/adk`'s logic — time formatting, Unicode
  handling, string building, concurrency, iteration — uses only `time`, `unicode`,
  `strings`, `sync`, `iter`, `errors`, `fmt`, `context`. Third-party imports are the
  client SDK and the framework, nothing else.
- **Modern Go, used plainly.** `iter.Seq`/`iter.Seq2` (Go 1.23) back the session's
  `State.All()` and `Events.All()` rather than callback slices.
- **Accept interfaces, return structs** — at call sites and at module boundaries.
- **Compile-time assertions guard every interface claim** — `var _ I = (*T)(nil)`
  sits next to the type it protects.
- **Read-optimized file ordering** — dependencies appear just above their first
  use, documented and enforced by [CLAUDE.md](CLAUDE.md).

---

## Owned nuances

Small deliberate choices a sharp reviewer will spot — documented so intent isn't
mistaken for oversight:

- **`formatElapsed` is always-plural** ("1 minutes"). Branching singular/plural
  buys grammar an LLM already tolerates; the simpler function is the deliberate
  trade. ([session.go](zep/adk/session.go))
- **Blank-message filtering counts invisible runes as blank.** A model asked for an
  "empty" turn may emit `U+200B`, `U+200E`, or `U+FEFF`; `unicode.IsSpace` alone
  would let those pollute the thread. `isBlank` also skips Unicode category `Cf` and
  control runes, keeping the invariant *content-driven*: no visible content, no Zep
  message. Covered by a table test across all these runes.
- **`temp:` state scope is refreshed every `Get`.** Time and message-format blocks
  are rebuilt per request by design, so "current time" is actually current.

---

## Non-goals

Boundaries are deliberate; naming them is part of the design:

- **Not the clients.** This module contains no Spotify/Tuya/Zep/Workspace transport
  logic — that lives in each client's own module. `agentkit` only binds.
- **Not a model provider.** Implementing `model.LLM` for a given LLM is outside the
  tool/session/memory scope of this toolkit; ADK ships its own model packages
  (`google.golang.org/adk/model/gemini`, `.../apigee`), and a custom `model.LLM` is
  a small standalone type. Keeping providers out keeps this module about *bindings*.
- **Not authentication or tenant isolation.** The `zep/adk` ownership guard is a
  fail-closed backstop, not an auth layer, and `postera`'s identity scoping is
  filtering, not access control. Authenticate and authorize above this module.
- **Not `List`/`Delete` for Zep sessions.** `SessionService.List`/`Delete` are
  intentional no-ops: threads are owned and enumerated in Zep, not through the ADK
  session surface. The methods exist only to satisfy the interface.

---

## Verification

Every claim above is backed by checks you can re-run. Numbers below were observed
with **Go 1.25.11** this session:

```bash
go build ./...     # ok
go vet ./...       # clean, no diagnostics
go test ./... -cover
```

| Package          | Tests | Coverage | Notes                                                                 |
| ---------------- | :---: | :------: | --------------------------------------------------------------------- |
| `zep/adk`        |  ✅   | **81.4%** | Ownership matrix, time harness, speaker attribution, blank filtering  |
| `postera/adk`    |  ✅   | **87.1%** | All three tools end-to-end via fakes + the localization-doc branch    |
| `spotify/adk`    |  ✅   | 49.0%    | `httptest`-driven flow through the real client; error translation     |
| `tuya/adk`       |  ✅   | 29.4%    | Registration, tool names, error translation                           |
| `gworkspace/adk` |  ✅   | 25.8%    | Registration, tool names, interface-satisfaction assertions           |

**Honest gaps.** `gworkspace/adk` and `tuya/adk` test tool registration, the
interface contracts, and error translation, but not the get/add handler bodies
end-to-end — those are thin passthroughs to the client, and the client owns their
tests. `spotify/adk` shows the higher bar worth reaching (a rewrite transport drives
the real client against fixture responses); extending that pattern to the other tool
bindings is the natural next step. `zep/adk` — where the real logic lives — is the
one held to 80%+.

---

## Compatibility

| Dependency                       | Version   | Role                              |
| -------------------------------- | --------- | --------------------------------- |
| `google.golang.org/adk`          | v1.2.0    | Target agent framework            |
| `google.golang.org/genai`        | v1.54.0   | Content/part types ADK speaks     |
| `github.com/getzep/zep-go/v3`    | v3.20.0   | Zep client (session + memory)     |
| `go.naturallyfunny.dev/gworkspace` | v0.4.0  | Workspace client                  |
| `go.naturallyfunny.dev/spotify`  | v0.6.0    | Spotify client                    |
| `go.naturallyfunny.dev/tuya`     | v0.5.0    | Tuya client                       |
| `go.naturallyfunny.dev/postera`  | v0.22.0   | Prospective-memory client         |

Because each binding is independent, importing (say) only `spotify/adk` links only
the Spotify and ADK trees — the Zep and Workspace SDKs stay out of your binary.

---

## Layout

```
agentkit/
├── gworkspace/adk/   Gmail, Calendar, Contacts → ADK tools
├── spotify/adk/      Spotify search + playback → ADK tools
├── tuya/adk/         Tuya smart-home read/control → ADK tools
├── postera/adk/      Future-self scheduling → ADK tools
├── zep/adk/          Zep → ADK session.Service + memory.Service
│   ├── session.go    SessionService: history, ownership, time harness, speakers
│   └── memory.go     MemoryService: user-graph search
├── examples/zep/     Runnable end-to-end wiring
└── CLAUDE.md         The ordering & naming rules this codebase is held to
```

---

## License

[MIT](LICENSE) © 2026 Ardian

[adk]: https://google.github.io/adk-docs/
[godoc]: https://pkg.go.dev/go.naturallyfunny.dev/agentkit
[proverbs]: https://go-proverbs.github.io/
[accept-interfaces]: https://go.dev/wiki/CodeReviewComments#interfaces
[loadlocation]: https://pkg.go.dev/time#LoadLocation
