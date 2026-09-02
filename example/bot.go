// Package example is a small Telegram bot: an echo, plus a settings menu that
// edits itself in place. It knows nothing about testing — the tests beside it
// drive it through the kitchen the way a consuming project would.
package example

import (
	"context"
	"strings"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

var languages = map[string]string{"en": "English", "fa": "فارسی"}

// New builds the bot against the given API base. Taking the base as an argument
// is the one thing a project must do to be testable: a bot that hardcodes
// Telegram's URL, or builds itself inside main, cannot be pointed at a kitchen.
func New(apiURL, token string) (*bot.Bot, error) {
	s := &store{settings: map[int64]settings{}}
	return bot.New(token, bot.WithServerURL(apiURL), bot.WithDefaultHandler(s.handle))
}

type settings struct {
	language string
	notify   bool
}

func (c settings) String() string {
	language := c.language
	if language == "" {
		language = "no language"
	}
	if c.notify {
		return language + ", notifications on"
	}
	return language + ", notifications off"
}

type store struct {
	mu       sync.Mutex
	settings map[int64]settings
}

func (s *store) get(chatID int64) settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings[chatID]
}

func (s *store) update(chatID int64, apply func(*settings)) settings {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.settings[chatID]
	apply(&c)
	s.settings[chatID] = c
	return c
}

func (s *store) handle(ctx context.Context, b *bot.Bot, u *models.Update) {
	switch {
	case u.CallbackQuery != nil:
		s.tapped(ctx, b, u.CallbackQuery)
	case u.Message == nil:
	case u.Message.Text == "/start":
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      u.Message.Chat.ID,
			Text:        "Welcome. Pick a language.",
			ReplyMarkup: languageKeyboard(),
		})
	default:
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: u.Message.Chat.ID,
			Text:   "echo: " + u.Message.Text,
		})
	}
}

func (s *store) tapped(ctx context.Context, b *bot.Bot, q *models.CallbackQuery) {
	b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})

	screen := q.Message.Message
	action, value, _ := strings.Cut(q.Data, ":")

	if action == "done" {
		b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    screen.Chat.ID,
			MessageID: screen.ID,
			Text:      "All set: " + s.get(screen.Chat.ID).String(),
		})
		return
	}

	updated := s.update(screen.Chat.ID, func(c *settings) {
		switch action {
		case "lang":
			c.language = languages[value]
		case "notify":
			c.notify = !c.notify
		}
	})

	b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      screen.Chat.ID,
		MessageID:   screen.ID,
		Text:        "Settings: " + updated.String(),
		ReplyMarkup: settingsKeyboard(updated),
	})
}

func languageKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: languages["en"], CallbackData: "lang:en"},
		{Text: languages["fa"], CallbackData: "lang:fa"},
	}}}
}

func settingsKeyboard(c settings) *models.InlineKeyboardMarkup {
	toggle := "Turn notifications on"
	if c.notify {
		toggle = "Turn notifications off"
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: toggle, CallbackData: "notify:toggle"}},
		{{Text: "Done", CallbackData: "done"}},
	}}
}
