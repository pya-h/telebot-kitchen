# telebot-kitchen

A test kitchen for Go Telegram bots. Drive a **real** bot through realistic
conversations in your tests — no network, no live token, no waiting on
Telegram — and assert on exactly what each user would see on their screen.

Testing a Telegram bot is a headache: the moving parts live behind Telegram's
servers, so most people fall back to poking the bot by hand, which is slow,
non-repeatable, and lets regressions slip through. telebot-kitchen replaces
Telegram with a fast, in-process fake so a conversation becomes an ordinary,
deterministic Go test.

```go
k := kitchen.New(t)                       // an in-process fake Telegram
bot := newYourBot(k.APIURL())             // point your bot's API base at the kitchen
k.DeliverToWebhook(bot.WebhookHandler())  // let the kitchen hand updates to your bot

alice := k.User(101)
alice.Send("/start")
require.True(t, alice.ExpectReply().HasButton("English"))

alice.Tap("English")                      // tap an inline button by its label
reply := alice.ExpectReply()              // wait for the answer, never sleep
require.Contains(t, reply.Text, "Welcome")
```

A full walkthrough — every verb, waiting instead of sleeping, scenarios, golden
transcripts and fault injection — is in [USAGE.md](USAGE.md).

## How it works

Every Go Telegram library talks to Telegram over the same HTTP API and can be
pointed at a custom base URL. telebot-kitchen stands up that API in-process:

- **Outbound** (bot → Telegram): the kitchen answers the Bot API methods your
  bot calls (`sendMessage`, `editMessageText`, `answerCallbackQuery`, …),
  updates an in-memory model of every chat, records the call, and returns a
  valid response — so your bot behaves exactly as it would in production.
- **Inbound** (user → bot): the kitchen injects updates the way Telegram would,
  either by posting to your webhook handler or by handing it straight to your
  update entry point. You never hand-craft an `Update`; you say
  `user.Tap("Next")`.

Because the seam is the HTTP protocol, the kitchen is **library-agnostic** — it
works with any Go bot library that can target a custom server URL and takes its
updates by webhook or through a "handle one update" entry point.

## What you can do with it

- **Virtual users and chats** — spin up as many users as a scenario needs; each
  has a private chat with its own screen and history.
- **Groups and channels** — a user inside a shared chat has the same verbs, its
  own place in the conversation, and the chat the bot actually branches on.
- **Membership and rights** — joins, leaves, edits, pins, bans and a bot kicked
  out, each as the update Telegram sends, and the permission errors that follow.
- **Multi-party flows** — two users talking *through* the bot is a first-class
  case, not an afterthought.
- **Tap by what you see** — press inline buttons by visible label or callback
  data; the kitchen finds them on the current screen for you.
- **Both keyboards** — inline buttons on a message, and the reply keyboard that
  outlives it: `Menu()` says what is up, `Press` pushes a key.
- **Albums** — one call, several messages under one group, sent and received.
- **Rich input** — text, commands, locations, and every kind of media a chat
  carries: photos, voice, audio, video, animations, documents, stickers and
  video notes, in both directions.
- **Screen & transcript rendering** — print a chat (inline keyboard and all) as
  text for debugging, golden tests, or human-readable acceptance evidence.
- **Markup, read the way Telegram reads it** — `MarkdownV2`, `Markdown` and
  `HTML` come off the text an assertion sees, and the spans they described are
  there to assert on.
- **Record and replay** — a live bot writes the updates it receives to a file;
  the kitchen hands them back, so a production incident becomes a test.
- **Three ways in, or a real port** — hand updates to a processor, post them to
  a webhook handler, queue them for `getUpdates`, or run the whole fake as a
  command so a bot in any language can be pointed at it and driven over HTTP.
- **Stars payments** — invoices, the pre-checkout handshake and refunds, with a
  ledger that says whether a charge was given back.
- **Fault injection** — make the fake API return `429`/`5xx`/flood-wait/timeouts
  on demand to exercise retry, backoff, and rate-limit handling.
- **No sleeps** — wait for what the bot did, not for the clock; replies sent
  from a worker goroutine are settled before your assertions run.
- **Concurrency rushes** — run a hundred conversations at once and catch the
  reply that lands in the wrong chat, which no race detector can see.
- **Load measurement** — drive the bot under pressure from a small binary and
  read throughput and per-step latency against the kitchen's own cost.
- **Deterministic by design** — message and update IDs are stable and the clock
  is injectable, so tests read the same way every run.

## Status

Both planned phases are in.

- **Phase A — core.** The engine, virtual users, the messaging and callback
  surface, screen rendering, fault injection, and the tooling that makes tests
  pleasant to write. This is the substance of the toolbox.
- **Phase B — completeness.** Broad Bot API coverage, group/channel/inline/
  payment/poll surfaces, record and replay, a standalone server binary, and
  library-agnostic delivery — so any Telegram bot has what it needs.

It is used in anger by a real bot, and what it grows next is what a real bot
turns out to need.

## Install

```sh
go get github.com/pya-h/telebot-kitchen
```

Requires Go 1.26+.

## License

TBD.
