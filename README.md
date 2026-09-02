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

## How it works

Every Go Telegram library talks to Telegram over the same HTTP API and can be
pointed at a custom base URL. telebot-kitchen stands up that API in-process:

- **Outbound** (bot → Telegram): the kitchen answers the Bot API methods your
  bot calls (`sendMessage`, `editMessageText`, `answerCallbackQuery`, …),
  updates an in-memory model of every chat, records the call, and returns a
  valid response — so your bot behaves exactly as it would in production.
- **Inbound** (user → bot): the kitchen injects updates the way Telegram would,
  either by posting to your webhook handler or by answering long-poll
  `getUpdates`. You never hand-craft an `Update`; you say `user.Tap("Next")`.

Because the seam is the HTTP protocol, the kitchen is **library-agnostic** — it
works with any Go bot library that can target a custom server URL and receives
updates by webhook or long-polling.

## What you can do with it

- **Virtual users and chats** — spin up as many users as a scenario needs; each
  has a private chat with its own screen and history.
- **Multi-party flows** — two users talking *through* the bot is a first-class
  case, not an afterthought.
- **Tap by what you see** — press inline buttons by visible label or callback
  data; the kitchen finds them on the current screen for you.
- **Rich input** — text, commands, photos, locations, contacts, and more.
- **Screen & transcript rendering** — print a chat (inline keyboard and all) as
  text for debugging, golden tests, or human-readable acceptance evidence.
- **Fault injection** — make the fake API return `429`/`5xx`/flood-wait/timeouts
  on demand to exercise retry, backoff, and rate-limit handling.
- **No sleeps** — wait for what the bot did, not for the clock; replies sent
  from a worker goroutine are settled before your assertions run.
- **Deterministic by design** — message and update IDs are stable and the clock
  is injectable, so tests read the same way every run.

## Status

Early, active development. Work is organized in two phases:

- **Phase A — core.** The engine, virtual users, the messaging and callback
  surface, screen rendering, fault injection, and the tooling that makes tests
  pleasant to write. This is the substance of the toolbox.
- **Phase B — completeness.** Broad Bot API coverage, group/channel/inline/
  payment/poll surfaces, a standalone server binary, and library adapters — so
  any Go Telegram bot has what it needs.

## Install

```sh
go get github.com/pya-h/telebot-kitchen
```

Requires Go 1.26+.

## License

TBD.
