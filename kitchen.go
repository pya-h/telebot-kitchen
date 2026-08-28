// Package kitchen stands up an in-process fake Telegram Bot API and drives a
// real bot through it, so a conversation becomes an ordinary Go test

// The bot under test is never modified; only the server it talks to is
// replaced. Point its API base at APIURL and give it Token.
package kitchen

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/go-telegram/bot/models"
)

const defaultToken = "1000000000:kitchen-test-token"

type TB interface {
	Cleanup(func())
	Errorf(format string, args ...any)
}

type Kitchen struct {
	tb     TB
	token  string
	server *httptest.Server

	mu      sync.RWMutex
	bot     models.User
	webhook webhook
}

type Option func(*Kitchen)

func WithToken(token string) Option { return func(k *Kitchen) { k.token = token } }

func WithBotName(name string) Option { return func(k *Kitchen) { k.bot.FirstName = name } }

func WithBotUsername(username string) Option {
	return func(k *Kitchen) { k.bot.Username = username }
}

func New(tb TB, opts ...Option) *Kitchen {
	k := &Kitchen{
		tb:    tb,
		token: defaultToken,
		bot:   models.User{IsBot: true, FirstName: "Kitchen", Username: "kitchen_bot"},
	}
	for _, opt := range opts {
		opt(k)
	}
	k.bot.ID = botIDFrom(k.token)

	k.server = httptest.NewServer(http.HandlerFunc(k.serve))
	tb.Cleanup(k.server.Close)
	return k
}

func (k *Kitchen) APIURL() string { return k.server.URL }

func (k *Kitchen) Token() string { return k.token }

func botIDFrom(token string) int64 {
	id, err := strconv.ParseInt(strings.SplitN(token, ":", 2)[0], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
