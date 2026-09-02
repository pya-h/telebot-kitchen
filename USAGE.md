# Using telebot-kitchen

## What it is

A fake Telegram, not a fake bot.

Your bot runs unmodified — its real handlers, its real state, its real calls to
the Bot API. Only the server on the other end of those calls is replaced, by an
in-process one that keeps a model of every chat and hands the bot updates the
way Telegram would. Nothing is stubbed inside your code, so a test exercises the
same paths production does.

## The one precondition

Your bot's construction has to be callable from a test, with the API base as an
argument:

```go
// New builds the bot against the given API base. Taking the base as an argument
// is the one thing a project must do to be testable: a bot that hardcodes
// Telegram's URL, or builds itself inside main, cannot be pointed at a kitchen.
func New(apiURL, token string) (*bot.Bot, error) {
	s := &store{settings: map[int64]settings{}}
	return bot.New(token, bot.WithServerURL(apiURL), bot.WithDefaultHandler(s.handle))
}
```

That is the whole adaptation. A bot that builds itself inside `main`, or reads
its URL from a package-level constant, has to grow this seam first.

## Your first test

```go
func start(t *testing.T) (*kitchen.Kitchen, *kitchen.User) {
	t.Helper()

	k := kitchen.New(t, kitchen.WithBotName("Settings Bot"))
	b, err := New(k.APIURL(), k.Token())
	if err != nil {
		t.Fatalf("build the bot: %v", err)
	}
	k.DeliverTo(b.ProcessUpdate)

	return k, k.User(101, kitchen.WithFullName("Ada", "Lovelace"))
}

func TestUnknownTextIsEchoed(t *testing.T) {
	k, ada := start(t)

	ada.Send("hello")
	ada.Expect(kitchen.TextIs("echo: hello"))
	ada.ExpectNothingMore()

	k.ExpectNo(kitchen.Method("editMessageText"))
}
```

`kitchen.New(t, …)` starts the fake server and tears it down through `t.Cleanup`.
`DeliverTo` binds the bot's "handle one update" entry point; a bot that only
exposes a webhook handler uses `DeliverToWebhook(h)` instead, and the kitchen
posts to it in process.

## Talking to the bot

Users are virtual people with their own private chat. `k.User(id, opts...)`
creates one on first mention and returns the same user afterwards.

| verb | what it does |
| --- | --- |
| `Send(text)` | a text message, with command entities parsed as Telegram parses them |
| `SendCommand(name, args...)` | `/name arg arg`, entities included |
| `Tap(labelOrData)` | presses an inline button by its visible label or its callback data |
| `SendPhoto(name, data, caption)` | an upload the bot can read back through `k.File` |
| `ShareLocation(lat, lng)` | a location message |

`Tap` looks at the user's current screen. If the label is not there it fails
with the buttons that were, so a renamed button reads as a clear failure rather
than a timeout. Passing callback data instead of a label survives translation:

```go
ada.Expect(
	kitchen.TextContains("Pick a language"),
	kitchen.HasButton("English"),
	kitchen.HasButton("lang:fa"),
)
```

By default only the newest keyboard in the chat answers a tap. `WithScrollback()`
lets a tap reach buttons on older messages.

## Reading what happened

Two sources, one vocabulary.

**The screen** is what the user would see: `ada.Screen()` for the newest message,
`ada.History()` for the whole chat, `k.History(chatID)` for any chat. A `Message`
carries `Text`, `From`, `Keyboard`, `Media`, `ForwardedFrom`, `Sent`, and prints
itself the way a client shows it.

**The record** is every call the bot made: `k.Calls()`, filtered with
`Matching` / `Count` / `Has` / `First` / `Last`. Rejected calls stay on it,
carrying the reason, and so does the library's own `getMe` handshake.

Both are matched with the same matchers: `Method`, `ToChat`, `ToUser`, `TextIs`,
`TextContains`, `HasButton`, `Param`, combined with `All` and `Any`. `Method` and
`Param` describe a call, so they never match a message on screen.

## Asserting

```go
ada.Expect(kitchen.TextIs("echo: hello"))   // wait for the next reply, assert on it
ada.ExpectScreen(kitchen.HasButton("Done")) // wait until the screen looks like this
ada.ExpectNothingMore()                     // nothing beyond the replies already read
k.Expect(kitchen.Method("sendMessage"))     // wait for a matching call, return the last
k.ExpectNo(kitchen.Method("deleteMessage"))
k.ExpectCount(3, kitchen.Method("answerCallbackQuery"))
```

`Expect` walks a user's replies in order — the first call takes the first reply,
the next takes the one after it. `ExpectScreen` is the one to reach for when a
bot edits its menu in place: no new message arrives, so there is no reply to
wait for, only a screen that changes.

Every assertion reports what it found against what it wanted, rendered:

```
kitchen: user 7 was told:
menu
[English] [فارسی]
want button "Deutsch"
```

## Waiting instead of sleeping

The library hands each update to your bot on its own goroutine unless you ask it
not to, so nothing has happened yet when `Send` returns. Never sleep. Every
assertion above already waits, and two primitives are there for the rest:

```go
k.WaitFor("the placeholder to be replaced", func() bool {
	return ada.Screen().Text == "done"
})
```

`WaitFor` rechecks its condition every time the bot calls the API or a user
speaks, so it costs nothing while the bot is working and fails with the
description you gave it if the condition never holds.

`k.Settle()` waits for the conversation to go quiet instead, for a bot that
replies from a worker pool or a paced queue. It is the blunt instrument — it can
only watch the clock, so prefer `WaitFor` whenever you can name what you are
waiting for. `ExpectNo`, `ExpectCount` and `ExpectNothingMore` settle for you,
because absence is only knowable once the bot stops working.

`WithWaitTimeout(d)` caps the wait; it defaults to two seconds.

## Scenarios

Steps run in order and share one kitchen. Once a step fails the rest are
skipped and named, because a later step assumes state the broken one never
produced:

```go
k.Scenario(
	kitchen.Step{Name: "open the menu", Do: func() {
		ada.SendCommand("start")
		ada.Expect(
			kitchen.TextContains("Pick a language"),
			kitchen.HasButton("English"),
			kitchen.HasButton("lang:fa"),
		)
	}},
	kitchen.Step{Name: "pick a language", Do: func() {
		ada.Tap("English")
		ada.ExpectScreen(
			kitchen.TextContains("English"),
			kitchen.HasButton("Turn notifications on"),
		)
	}},
	kitchen.Step{Name: "finish", Do: func() {
		ada.Tap("Done")
		ada.ExpectScreen(kitchen.TextIs("All set: English, notifications on"))
	}},
)
```

## What carries between tests, and what does not

One kitchen per test. A kitchen owns its chats, message ids, files, callback
answers, call record, clock and faults; nothing crosses between two of them, and
each is torn down with the test that made it.

Your bot's own state is a different matter. `start` builds a fresh bot per test,
so the settings store starts empty. A bot built once and shared across tests
carries its state along with it — that is your bot's behaviour, not the
kitchen's, and it is usually worth avoiding.

Within one kitchen, everything accumulates: the record holds every call from the
first one, and a user's chat holds every message. Users are distinct by id, so
one user's screen is never another's:

```go
func TestSettingsAreKeptPerUser(t *testing.T) {
	k, ada := start(t)
	grace := k.User(102, kitchen.WithFullName("Grace", "Hopper"))

	for _, user := range []*kitchen.User{ada, grace} {
		user.SendCommand("start")
		user.Expect(kitchen.HasButton("English"))
	}

	ada.Tap("فارسی")
	ada.ExpectScreen(kitchen.TextContains("فارسی"))

	grace.Tap("English")
	grace.ExpectScreen(kitchen.TextContains("English"))

	// Ada acts last, so a setting leaking between chats would surface as Grace's
	// language on Ada's screen.
	ada.Tap("Turn notifications on")
	ada.ExpectScreen(kitchen.TextIs("Settings: فارسی, notifications on"))

	k.ExpectCount(1, kitchen.Method("editMessageText"), kitchen.ToUser(grace))
}
```

## Transcripts and golden files

`ada.Transcript()` renders the chat as a readable back-and-forth, and
`ExpectTranscript` compares it with a file that `-kitchen.update` rewrites:

```go
func TestTheWholeConversationReads(t *testing.T) {
	_, ada := start(t)

	ada.SendCommand("start")
	ada.Expect(kitchen.HasButton("English"))

	ada.Tap("English")
	ada.ExpectScreen(kitchen.HasButton("Done"))

	ada.Tap("Turn notifications on")
	ada.ExpectScreen(kitchen.TextContains("notifications on"))

	ada.Tap("Done")
	ada.ExpectScreen(kitchen.TextContains("All set"))

	ada.ExpectTranscript(filepath.Join("testdata", "settings.md"))
}
```

```sh
go test ./... -kitchen.update    # rewrite every golden the run touches
```

A transcript is the chat's final state, not a replay: a menu edited in place
shows where it landed, not every version it passed through. Use it to catch a
change in wording or layout, and step assertions to catch a change mid-flow.
`k.ExpectGolden(path, text)` does the same for anything else you can render.

## Fault injection

Make the fake API refuse a call, and watch what the bot does about it:

```go
func TestAFloodWaitCostsTheMenu(t *testing.T) {
	k, ada := start(t)
	k.FailOnce(kitchen.TooManyRequests(time.Second), kitchen.Method("sendMessage"))

	ada.SendCommand("start")

	refused := k.Expect(kitchen.Method("sendMessage"))
	if !strings.Contains(refused.Error, "Too Many Requests") {
		t.Errorf("call = %+v, want the flood wait recorded against the attempt", refused)
	}
	ada.ExpectNothingMore()
}
```

The faults are `TooManyRequests(retryAfter)`, `ServerError()`, `Malformed()` —
a reply no client can decode — and `Timeout()`, which drops the connection
rather than holding it open, so the test meets the failure at once instead of
waiting out the bot's own client timeout.

Scope a fault with the same matchers the record uses, and choose how long it
stands:

```go
k.Fail(fault, ms...)         // every matching call, until cleared
k.FailOnce(fault, ms...)     // one call, the shape a retry recovers from
k.FailAfter(2, fault, ms...) // let two through, refuse the rest
```

`Fail` returns a handle whose `Clear()` takes that one fault down;
`k.ClearFaults()` takes them all. A refused call never reaches the world — the
chat is left untouched, exactly as Telegram leaves it — but it stays on the
record with the refusal against it.

## Relaying between users

`forwardMessage` and `copyMessage` are modelled down to their return types: a
forward comes back as a message, a copy as a bare id. A forward carries where it
came from and loses its inline keyboard; a copy carries neither, and takes only
the caption and keyboard the call gives it.

`ForwardedFrom` is what tells them apart on the receiving screen:

```go
screen := bob.ExpectScreen(kitchen.TextIs("hello"))
if got := screen.String(); got != "(forwarded from Ada Lovelace) hello" {
	t.Errorf("Bob sees %q, want the forward marked as a client shows it", got)
}
```

## Determinism

Message, update and file ids are monotonic per kitchen, and the clock only moves
when a test moves it:

```go
k.Clock().Advance(24 * time.Hour)
```

Nothing in the toolbox reads the wall clock — a test enforces that — so two runs
of the same test produce the same ids, dates and transcripts.

Options on `New`: `WithBotName`, `WithBotUsername`, `WithToken`, `WithStartTime`,
`WithWaitTimeout`, `WithScrollback`. Options on `User`: `WithFullName`,
`WithUsername`, `WithLanguage`.

## Not yet here

Long-poll `getUpdates` delivery — webhook and direct modes cover both ways a bot
is normally driven — and the stress and concurrency harness. This document grows
with the surface, and describes only what ships today.
