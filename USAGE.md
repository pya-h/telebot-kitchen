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

## Groups and channels

A user's private chat is theirs alone. A shared one is registered first, with an
id Telegram would give it — negative, and a positive one is refused:

```go
team := k.Supergroup(-1001234567890, "Standup")   // also k.Group and k.Channel
```

`ada.In(team)` is Ada inside that chat, and carries the same verbs the plain user
has. Each chat keeps its own place in the conversation, so what Ada has already
read in one says nothing about the other:

```go
ada.Send("hello")                          // her private chat
ada.Expect(kitchen.TextIs("hi Ada"))

ada.In(team).SendCommand("standup")
ada.In(team).Expect(kitchen.TextContains("Who is first?"))
bob.In(team).Expect(kitchen.TextContains("Who is first?"))   // both were told
```

A chat's message carries the group rather than a person: `Chat.Title` and
`Chat.Type` are what the bot branches on, and `From` is the member who spoke.
`team.Members()` lists who is in it, `team.History()` and `team.Transcript()`
read it from outside any one member.

### Arriving and leaving

`In` says where somebody is; it is not an event. `Join` and `Leave` are the
event, and the bot hears them the way Telegram sends them — the service message
in the chat, then the membership update:

```go
ada.In(team).Join()     // new_chat_members, then chat_member
ada.In(team).Leave()
```

`ada.In(team).Edit(sent, "what I meant")` is the same idea for a message: the
member rewording what they said, which the bot hears as `edited_message`. Only
their own message is theirs to edit.

### Rights, and the failures that come with them

The bot is an administrator with every right the moment a chat exists, because
that is how one is set up before anybody thinks to test it. A chat where it
cannot work is what you opt into, and the bot hears that as `my_chat_member`:

```go
ada.In(news).PromoteBot(kitchen.PinMessages)   // and no right to post
ada.In(team).DemoteBot()                       // an ordinary member
ada.In(team).RemoveBot()                       // kicked out
```

What the bot then meets is what it meets in production: `403 Forbidden` for a
chat it is out of, `need administrator rights in the channel chat` for a post it
may not make, `not enough rights to pin a message`, and `message can't be
deleted for everyone` for somebody else's. Its own message is always its own to
take back.

`ada.In(team).Promote(bob, kitchen.PinMessages)` and `Demote(bob)` do the same
for people, which is what a bot reads back through `getChatMember`. `getChat`,
`getChatAdministrators` and `getChatMemberCount` answer from the same roster,
and the bot changes it itself through `banChatMember`, `unbanChatMember`,
`restrictChatMember`, `promoteChatMember`, `pinChatMessage` and the unpins. A
member the bot has restricted is not heard, so a test cannot go on talking
through a silence the bot asked for.

`PostMessages`, `EditMessages`, `DeleteMessages`, `PinMessages`,
`RestrictMembers` and `PromoteMembers` are enforced. `InviteUsers` and
`ChangeInfo` are reported through `getChatMember` and nothing more, since no
call here can be refused for them.

Nothing the bot does comes back to it: its own message, edit, pin or ban makes
no update, exactly as its own `sendMessage` never did.

### When a group becomes a supergroup

`team.MigrateToSupergroup(-1001234567890)` strands every chat id the bot stored.
Calls to the old chat fail the way Telegram fails them, naming the new one in
`migrate_to_chat_id`, and both chats record the move.

### Channels

Nobody speaks in a channel: the bot posts through the API, and an admin posting
from a client is `news.Post(text)`, which the bot hears as `channel_post`.
`news.EditPost(post, text)` is that post being reworded, heard as
`edited_channel_post`. What the bot itself sends does not come back to it as a
post, so a bot that mirrors a channel cannot chase its own tail. A member trying
to speak there fails with that.

## Reading what happened

Two sources, one vocabulary.

**The screen** is what the user would see: `ada.Screen()` for the newest message,
`ada.History()` for the whole chat, `k.History(chatID)` for any chat. In a shared
chat the same verbs hang off `ada.In(team)`. A `Message`
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

The faults are `TooManyRequests(retryAfter)`, `Blocked()` — the user shutting
the bot out, which it only ever learns from the next thing it sends —
`ServerError()`, `Malformed()`, a reply no client can decode, and `Timeout()`,
which drops the connection rather than holding it open, so the test meets the
failure at once instead of waiting out the bot's own client timeout.

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

## Rushes

Concurrency breaks bots in ways one conversation never shows: a reply in the
wrong chat, a reply that never comes, one that comes twice, a verb that never
settles. `-race` sees none of them, because none of them is a data race — they
are wrong logic, and the bot is perfectly synchronised while it does the wrong
thing.

A rush runs many short conversations at once, each in a kitchen of its own:

```go
func TestTheMenuHoldsUpUnderLoad(t *testing.T) {
	kitchen.Rush{
		Orders:      120,
		Concurrency: 12,
		Bot: func(k *kitchen.Kitchen) error {
			b, err := New(k.APIURL(), k.Token())
			if err != nil {
				return err
			}
			k.DeliverTo(b.ProcessUpdate)
			return nil
		},
		Serve: func(o *kitchen.Order) {
			ada := o.User(101, kitchen.WithFullName("Ada", "Lovelace"))

			// The echo carries the ticket, which is what the cross-chat check reads.
			ada.Send(o.Ticket)
			ada.Expect(kitchen.TextIs("echo: " + o.Ticket))

			ada.SendCommand("start")
			ada.Expect(kitchen.HasButton("English"))

			ada.Tap("English")
			ada.ExpectScreen(kitchen.HasButton("Done"))

			ada.Tap("Done")
			ada.ExpectScreen(kitchen.TextContains("All set"))
			ada.ExpectNothingMore()
		},
	}.Run(t)
}
```

```sh
go test ./... -kitchen.stress    # an ordinary run skips every rush
```

`Order` carries the whole kitchen, so the script is written with the verbs you
already know — and that is where two of the four checks come from: a lost reply
is an `Expect` that times out, a duplicate is what `ExpectNothingMore` catches.
The harness owns the two a script cannot see for itself. `o.Ticket` belongs to
this order alone; put it in what your users say, and the rush reads every chat
afterwards looking for somebody else's — which is how a reply landing in the
wrong conversation is caught. An order that never finishes is reported as a
stuck verb rather than hanging the run.

Every broken order is reported together, with the command that replays one:

```
kitchen: 7 of 120 orders broke at 12-way concurrency

  order 8:
    kitchen: user 101 was told:
    echo: ticket-2
    want text "echo: ticket-8"
    kitchen: chat 101 was told "echo: ticket-2", which belongs to another conversation

  replay it: go test -run <this test> -kitchen.stress -kitchen.seed=1 -kitchen.order=8
```

`Seed` drives `o.Rand`, so a script that varies itself varies the same way on a
replay. `Options` are passed to every kitchen in the rush, and `Timeout` caps one
order. Sockets, not memory, are the ceiling — each kitchen opens a real listener,
so `Concurrency` is the knob that matters.

## Measuring under load

A rush asks whether the bot is correct under concurrency. The load runner asks
what it costs, and it lives outside the toolbox — in `load`, which may read the
real clock the kitchen itself may not.

It is a library, not a command: it cannot know your conversations, so you write
a small `main.go` holding nothing but yours.

```go
report := load.Run{
	Orders:      200,
	Concurrency: 16,
	Bot: func(k *kitchen.Kitchen) error {
		b, err := example.New(k.APIURL(), k.Token())
		if err != nil {
			return err
		}
		k.DeliverTo(b.ProcessUpdate)
		return nil
	},
	Serve: func(o *load.Order) {
		ada := o.User(101, kitchen.WithFullName("Ada", "Lovelace"))

		o.Step("echo", func() {
			ada.Send(o.Ticket)
			ada.Expect(kitchen.TextIs("echo: " + o.Ticket))
		})
		o.Step("open the menu", func() {
			ada.SendCommand("start")
			ada.Expect(kitchen.HasButton("English"))
		})
	},
}.Measure()

fmt.Print(report)
```

`o.Step(name, fn)` times a part of the conversation, so the report can say where
the time went. Everything else is the ordinary verbs again.

```
200 orders at 16-way concurrency in 185.903ms — 1076 orders/sec

  conversation           n 200   p50 12.192ms  p95 33.815ms  max 41.22ms
  kitchen alone          n 200   p50 62µs      p95 445µs     max 1.763ms
  net of the kitchen     p50 12.131ms

  step
  echo                   n 200   p50 1.269ms   p95 4.478ms   max 30.674ms
  open the menu          n 200   p50 1.439ms   p95 4.924ms   max 26.835ms

  peak goroutines 147
  every order came back clean
```

The `kitchen alone` line is the point of the exercise: the same lifecycle with no
bot in it, so you can tell what the numbers belong to. Above, the harness costs
62µs against a 12ms conversation, so the time is the bot's. Orders that break are
counted by kind — `assertion`, `stuck`, `build` — rather than reported one by
one, which is what a rush is for.

Read them for what they are. Nothing here crosses a network, so the figures
describe handler and datastore cost, not Telegram. And a bot that paces itself
still sleeps for real, so what you measure is its own pacing under load rather
than a throughput ceiling.

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

Long-poll `getUpdates` delivery, deferred until a polling consumer needs it:
webhook and direct modes cover both ways a bot is normally driven, and a poll is
reported as an unsupported method rather than silently hanging. This document
grows with the surface, and describes only what ships today.
