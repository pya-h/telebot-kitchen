package kitchen

import (
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
}

func newWorld(clock *Clock) *world {
	return &world{clock: clock, chats: map[int64]*chat{}}
}

func (w *world) nextUpdate() int64 { return w.nextUpdateID.Add(1) }

func (w *world) chatAt(id int64) *chat {
	c, ok := w.chats[id]
	if !ok {
		c = &chat{info: models.Chat{ID: id, Type: models.ChatTypePrivate}, nextMessageID: 1}
		w.chats[id] = c
	}
	return c
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

	c, ok := w.chats[chatID]
	if !ok {
		return models.Message{}, false
	}
	for _, m := range c.messages {
		if m.ID == messageID {
			return *m, true
		}
	}
	return models.Message{}, false
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
