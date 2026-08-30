package kitchen

import (
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/go-telegram/bot/models"
)

type chat struct {
	info          models.Chat
	nextMessageID int
	messages      []*models.Message
}

type world struct {
	clock *Clock

	mu    sync.RWMutex
	chats map[int64]*chat

	nextUpdateID atomic.Int64
	nextQueryID  atomic.Int64
}

func newWorld(clock *Clock) *world {
	return &world{clock: clock, chats: map[int64]*chat{}}
}

func (w *world) nextUpdate() int64 { return w.nextUpdateID.Add(1) }

func (w *world) nextQuery() string { return "query-" + strconv.FormatInt(w.nextQueryID.Add(1), 10) }

func (w *world) chatAt(id int64) *chat {
	c, ok := w.chats[id]
	if !ok {
		c = &chat{info: models.Chat{ID: id, Type: models.ChatTypePrivate}, nextMessageID: 1}
		w.chats[id] = c
	}
	return c
}

// A private chat mirrors the identity of the user it belongs to.
func (w *world) join(u models.User) {
	w.mu.Lock()
	defer w.mu.Unlock()

	c := w.chatAt(u.ID)
	c.info.FirstName, c.info.LastName, c.info.Username = u.FirstName, u.LastName, u.Username
}

func (w *world) add(chatID int64, m models.Message) models.Message {
	w.mu.Lock()
	defer w.mu.Unlock()

	c := w.chatAt(chatID)
	m.ID = c.nextMessageID
	m.Chat = c.info
	m.Date = int(w.clock.Now().Unix())
	c.nextMessageID++

	c.messages = append(c.messages, &m)
	return m
}

func (w *world) message(chatID int64, messageID int) (models.Message, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	m := w.find(chatID, messageID)
	if m == nil {
		return models.Message{}, false
	}
	return *m, true
}

func (w *world) find(chatID int64, messageID int) *models.Message {
	c, ok := w.chats[chatID]
	if !ok {
		return nil
	}
	for _, m := range c.messages {
		if m.ID == messageID {
			return m
		}
	}
	return nil
}

// edit reports ok when the message exists and changed when mutate found
// anything to alter; an edit that changes nothing is an error to Telegram.
func (w *world) edit(chatID int64, messageID int, mutate func(*models.Message) bool) (edited models.Message, changed, ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	m := w.find(chatID, messageID)
	if m == nil {
		return models.Message{}, false, false
	}
	if !mutate(m) {
		return *m, false, true
	}
	m.EditDate = int(w.clock.Now().Unix())
	return *m, true, true
}

func (w *world) remove(chatID int64, messageID int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	c, ok := w.chats[chatID]
	if !ok {
		return false
	}
	for i, m := range c.messages {
		if m.ID == messageID {
			c.messages = append(c.messages[:i], c.messages[i+1:]...)
			return true
		}
	}
	return false
}

// keyboards returns the chat's messages that still carry an inline keyboard,
// newest first; at most limit of them, or every one when limit is zero.
func (w *world) keyboards(chatID int64, limit int) []models.Message {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return nil
	}
	var screens []models.Message
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].ReplyMarkup == nil {
			continue
		}
		screens = append(screens, *c.messages[i])
		if len(screens) == limit {
			break
		}
	}
	return screens
}

func (w *world) latest(chatID int64) (models.Message, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok || len(c.messages) == 0 {
		return models.Message{}, false
	}
	return *c.messages[len(c.messages)-1], true
}

func (w *world) history(chatID int64) []models.Message {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return nil
	}
	log := make([]models.Message, len(c.messages))
	for i, m := range c.messages {
		log[i] = *m
	}
	return log
}
