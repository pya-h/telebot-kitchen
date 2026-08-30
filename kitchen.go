// Package kitchen stands up an in-process fake Telegram Bot API and drives a
// real bot through it, so a conversation becomes an ordinary Go test.
//
// The bot under test is never modified; only the server it talks to is
// replaced. Point its API base at APIURL and give it Token.
package kitchen

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
)

const defaultToken = "1000000000:kitchen-test-token"

type TB interface {
	Cleanup(func())
	Errorf(format string, args ...any)
}

type Kitchen struct {
	tb        TB
	token     string
	server    *httptest.Server
	clock     *Clock
	world     *world
	files     *mediaStore
	callbacks *callbackLog

	deliverMu sync.Mutex

	mu      sync.RWMutex
	bot     models.User
	webhook webhook
	process UpdateProcessor
	hook    http.Handler
	users   map[int64]*User
}

type Option func(*Kitchen)

func WithToken(token string) Option { return func(k *Kitchen) { k.token = token } }

func WithBotName(name string) Option { return func(k *Kitchen) { k.bot.FirstName = name } }

func WithBotUsername(username string) Option {
	return func(k *Kitchen) { k.bot.Username = username }
}

func WithStartTime(t time.Time) Option { return func(k *Kitchen) { k.clock.now = t } }

func New(tb TB, opts ...Option) *Kitchen {
	k := &Kitchen{
		tb:        tb,
		token:     defaultToken,
		clock:     &Clock{now: defaultStartTime},
		files:     newMediaStore(),
		callbacks: newCallbackLog(),
		users:     map[int64]*User{},
		bot:       models.User{IsBot: true, FirstName: "Kitchen", Username: "kitchen_bot"},
	}
	for _, opt := range opts {
		opt(k)
	}
	k.bot.ID = botIDFrom(k.token)
	k.world = newWorld(k.clock)

	k.server = httptest.NewServer(http.HandlerFunc(k.serve))
	tb.Cleanup(k.server.Close)
	return k
}

func (k *Kitchen) APIURL() string { return k.server.URL }

func (k *Kitchen) Token() string { return k.token }

func (k *Kitchen) Clock() *Clock { return k.clock }

// File returns an upload the bot sent, by the file id the kitchen issued for it.
func (k *Kitchen) File(fileID string) (File, bool) { return k.files.get(fileID) }

func (k *Kitchen) CallbackAnswer(queryID string) (CallbackAnswer, bool) {
	return k.callbacks.byID(queryID)
}

func (k *Kitchen) CallbackAnswers() []CallbackAnswer { return k.callbacks.all() }

func botIDFrom(token string) int64 {
	id, err := strconv.ParseInt(strings.SplitN(token, ":", 2)[0], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
