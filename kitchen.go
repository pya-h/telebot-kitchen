// Package kitchen stands up an in-process fake Telegram Bot API and drives a
// real bot through it, so a conversation becomes an ordinary Go test.
package kitchen

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
)

const (
	defaultToken = "1000000000:kitchen-test-token"

	// A bot without an id would read as an unknown sender, so a token the kitchen
	// cannot take one from falls back to this.
	fallbackBotID = 1000000000
)

type TB interface {
	Cleanup(func())
	Errorf(format string, args ...any)
	Failed() bool
}

type Kitchen struct {
	tb         TB
	token      string
	scrollback bool
	server     *httptest.Server
	clock      *Clock
	world      *world
	files      *mediaStore
	callbacks  *callbackLog
	calls      *recorder
	payments   *ledger
	polls      *pollIndex
	reactions  *reactionBook
	joins      *joinBook
	boosts     *boostBook
	inline     *inlineBook
	updates    *pollQueue
	faults     *faultStore
	activity   *activity

	unsupported   sync.Map
	polledUnbound sync.Once

	// Closed when the test is over, so a poll waiting on nothing gives up rather
	// than holding the server open until it has waited its whole timeout out.
	closing chan struct{}

	address     string
	waitTimeout time.Duration

	deliverMu sync.Mutex

	mu      sync.RWMutex
	bot     models.User
	webhook webhook
	process UpdateProcessor
	polling bool
	wire    bool
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

// WithWaitTimeout caps how long the await primitives block before failing.
func WithWaitTimeout(d time.Duration) Option { return func(k *Kitchen) { k.waitTimeout = d } }

func WithAddress(addr string) Option { return func(k *Kitchen) { k.address = addr } }

// WithScrollback lets a tap reach buttons on older messages; without it only the newest keyboard answers.
func WithScrollback() Option { return func(k *Kitchen) { k.scrollback = true } }

func New(tb TB, opts ...Option) *Kitchen {
	k := &Kitchen{
		tb:          tb,
		token:       defaultToken,
		clock:       &Clock{now: defaultStartTime},
		files:       newMediaStore(),
		callbacks:   newCallbackLog(),
		calls:       newRecorder(),
		payments:    newLedger(),
		polls:       newPollIndex(),
		reactions:   newReactionBook(),
		joins:       newJoinBook(),
		boosts:      newBoostBook(),
		inline:      newInlineBook(),
		updates:     newPollQueue(),
		faults:      newFaultStore(),
		activity:    newActivity(),
		closing:     make(chan struct{}),
		waitTimeout: defaultWaitTimeout,
		users:       map[int64]*User{},
		bot:         models.User{IsBot: true, FirstName: "Kitchen", Username: "kitchen_bot"},
	}
	for _, opt := range opts {
		opt(k)
	}
	k.bot.ID = botIDFrom(k.token)
	k.world = newWorld(k.clock, k.bot)

	k.server = httptest.NewUnstartedServer(http.HandlerFunc(k.serve))
	if k.address != "" {
		if listener, err := net.Listen("tcp", k.address); err != nil {
			tb.Errorf("kitchen: listen on %s: %v", k.address, err)
		} else {
			k.server.Listener.Close()
			k.server.Listener = listener
		}
	}
	k.server.Start()
	tb.Cleanup(k.close)
	return k
}

func (k *Kitchen) close() {
	close(k.closing)
	k.server.Close()
}

func (k *Kitchen) APIURL() string { return k.server.URL }

func (k *Kitchen) Token() string { return k.token }

func (k *Kitchen) Clock() *Clock { return k.clock }

// File reads a file back by any id it goes by, each size of a photo included.
func (k *Kitchen) File(fileID string) (File, bool) { return k.files.get(fileID) }

// Upload holds a file as if it had reached Telegram before the test began, for a
// bot whose storage already names it by id. kind is the Bot API's word for it.
func (k *Kitchen) Upload(kind, name string, data []byte) File {
	if _, known := fileKinds[kind]; !known {
		names := make([]string, len(mediaKinds))
		for i, each := range mediaKinds {
			names[i] = each.param
		}
		k.tb.Errorf("kitchen: %q is not a kind of file, want one of %s", kind, strings.Join(names, ", "))
		return File{}
	}
	return k.files.issue(kind, name, data)
}

func (k *Kitchen) CallbackAnswer(queryID string) (CallbackAnswer, bool) {
	return k.callbacks.byID(queryID)
}

func (k *Kitchen) CallbackAnswers() []CallbackAnswer { return k.callbacks.all() }

func (k *Kitchen) reach() int {
	if k.scrollback {
		return 0
	}
	return 1
}

// Telegram puts the bot's id in front of the colon.
func botIDFrom(token string) int64 {
	id, err := strconv.ParseInt(strings.SplitN(token, ":", 2)[0], 10, 64)
	if err != nil || id <= 0 {
		return fallbackBotID
	}
	return id
}
