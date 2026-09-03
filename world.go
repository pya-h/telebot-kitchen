package kitchen

import (
	"maps"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/go-telegram/bot/models"
)

type chat struct {
	info models.Chat
	// Kept apart from members, who are people.
	bot           standing
	members       map[int64]*standing
	nextMessageID int
	messages      []*models.Message
}

type world struct {
	clock *Clock
	bot   models.User

	mu    sync.RWMutex
	chats map[int64]*chat

	nextUpdateID atomic.Int64
	nextQueryID  atomic.Int64
}

func newWorld(clock *Clock, bot models.User) *world {
	return &world{clock: clock, bot: bot, chats: map[int64]*chat{}}
}

func (w *world) nextUpdate() int64 { return w.nextUpdateID.Add(1) }

func (w *world) nextQuery() string { return "query-" + strconv.FormatInt(w.nextQueryID.Add(1), 10) }

// chatAt is for an id nobody described, which only a private chat can be.
func (w *world) chatAt(id int64) *chat {
	return w.chatOf(id, models.ChatTypePrivate, "", w.bot)
}

func (w *world) chatOf(id int64, kind models.ChatType, title string, bot models.User) *chat {
	c, ok := w.chats[id]
	if !ok {
		c = &chat{
			info:          models.Chat{ID: id, Type: kind},
			bot:           botStanding(kind, bot),
			members:       map[int64]*standing{},
			nextMessageID: 1,
		}
		w.chats[id] = c
	}
	if title != "" {
		c.info.Title = title
	}
	return c
}

// A bot starts out an administrator: a chat it cannot work in is opted into.
func botStanding(kind models.ChatType, bot models.User) standing {
	if kind == models.ChatTypePrivate {
		return standing{user: bot, status: models.ChatMemberTypeMember}
	}
	return standing{user: bot, status: models.ChatMemberTypeAdministrator, rights: everyRight}
}

func (w *world) register(id int64, kind models.ChatType, title string, bot models.User) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.chatOf(id, kind, title, bot)
}

// restand moves somebody, handing back the pair a membership update carries.
func (w *world) restand(chatID int64, who models.User, to standing) (models.ChatMember, models.ChatMember, models.Chat) {
	w.mu.Lock()
	defer w.mu.Unlock()

	c := w.chatAt(chatID)
	was := standing{user: who, status: models.ChatMemberTypeLeft}
	if s, ok := c.members[who.ID]; ok {
		was = *s
	}
	to.user = who
	c.members[who.ID] = &to
	return was.chatMember(), to.chatMember(), c.info
}

// restandBot is the same for the bot.
func (w *world) restandBot(chatID int64, to standing) (models.ChatMember, models.ChatMember, models.Chat) {
	w.mu.Lock()
	defer w.mu.Unlock()

	c := w.chatAt(chatID)
	was := c.bot
	to.user = was.user
	c.bot = to
	return was.chatMember(), to.chatMember(), c.info
}

func (w *world) standingOf(chatID, userID int64) (models.ChatMember, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return models.ChatMember{}, false
	}
	if c.bot.user.ID == userID {
		return c.bot.chatMember(), true
	}
	if s, ok := c.members[userID]; ok {
		return s.chatMember(), true
	}
	return models.ChatMember{}, false
}

func (w *world) administrators(chatID int64) []*models.ChatMember {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return nil
	}
	var admins []*models.ChatMember
	for _, s := range append([]*standing{&c.bot}, presentBy(c, slices.Sorted(maps.Keys(c.members)))...) {
		if s.status == models.ChatMemberTypeAdministrator || s.status == models.ChatMemberTypeOwner {
			member := s.chatMember()
			admins = append(admins, &member)
		}
	}
	return admins
}

func presentBy(c *chat, ids []int64) []*standing {
	members := make([]*standing, 0, len(ids))
	for _, id := range ids {
		members = append(members, c.members[id])
	}
	return members
}

func (w *world) info(chatID int64) (models.Chat, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return models.Chat{}, false
	}
	return c.info, true
}

// join puts a user on the roster without announcing it. A private chat also
// mirrors their identity, since to Telegram that chat is them.
func (w *world) join(chatID int64, u models.User) {
	w.mu.Lock()
	defer w.mu.Unlock()

	c := w.chatAt(chatID)
	if _, ok := c.members[u.ID]; !ok {
		c.members[u.ID] = &standing{status: models.ChatMemberTypeMember}
	}
	c.members[u.ID].user = u
	if c.info.Type == models.ChatTypePrivate {
		c.info.FirstName, c.info.LastName, c.info.Username = u.FirstName, u.LastName, u.Username
	}
}

// roster lists who is still in the chat, in id order.
func (w *world) roster(chatID int64) []int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return nil
	}
	var ids []int64
	for _, id := range slices.Sorted(maps.Keys(c.members)) {
		if c.members[id].present() {
			ids = append(ids, id)
		}
	}
	return ids
}

func (w *world) title(chatID int64) string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return ""
	}
	return c.info.Title
}

func (w *world) add(chatID int64, m models.Message) models.Message {
	w.mu.Lock()
	defer w.mu.Unlock()

	c := w.chatAt(chatID)
	m.ID = c.nextMessageID
	m.Chat = c.info
	m.Date = int(w.clock.Now().Unix())
	// A message in a channel is published by the channel.
	if c.info.Type == models.ChatTypeChannel {
		posted := c.info
		m.SenderChat = &posted
	}
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

// edit reports whether the message exists; mutate returns the error Telegram
// would raise, so an edit that changes nothing rejects the same way.
func (w *world) edit(chatID int64, messageID int, mutate func(*models.Message) error) (edited models.Message, found bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	m := w.find(chatID, messageID)
	if m == nil {
		return models.Message{}, false, nil
	}
	if err := mutate(m); err != nil {
		return *m, true, err
	}
	m.EditDate = int(w.clock.Now().Unix())
	return *m, true, nil
}

// remove reports whether the message exists, and why the bot may not take it back.
func (w *world) remove(chatID int64, messageID int, botID int64) (found bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	c, ok := w.chats[chatID]
	if !ok {
		return false, nil
	}
	for i, m := range c.messages {
		if m.ID != messageID {
			continue
		}
		if err := c.mayDelete(m, botID); err != nil {
			return true, err
		}
		c.messages = append(c.messages[:i], c.messages[i+1:]...)
		return true, nil
	}
	return false, nil
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

func (w *world) chatIDs() []int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	ids := make([]int64, 0, len(w.chats))
	for id := range w.chats {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
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
