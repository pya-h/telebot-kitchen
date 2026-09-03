package kitchen

import (
	"maps"
	"net/http"
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
	pinned        []int
	movedTo       int64
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

	w.place(w.chatAt(chatID), u)
}

func (w *world) place(c *chat, u models.User) *standing {
	s, ok := c.members[u.ID]
	if !ok {
		s = &standing{status: models.ChatMemberTypeMember}
		c.members[u.ID] = s
	}
	s.user = u
	if c.info.Type == models.ChatTypePrivate {
		c.info.FirstName, c.info.LastName, c.info.Username = u.FirstName, u.LastName, u.Username
	}
	return s
}

// speaking places the sender, unless the bot has shut them up.
func (w *world) speaking(chatID int64, u models.User) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	c := w.chatAt(chatID)
	if s, ok := c.members[u.ID]; ok && (s.silenced || s.status == models.ChatMemberTypeBanned) {
		return false
	}
	w.place(c, u)
	return true
}

// manage changes a member's standing on the bot's say-so, if it may.
func (w *world) manage(chatID, userID int64, need Right, what string, apply func(*standing)) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	c, ok := w.chats[chatID]
	if !ok {
		return requestError("chat not found")
	}
	if err := c.mayManage(need, what); err != nil {
		return err
	}
	s, ok := c.members[userID]
	if !ok {
		return requestError("user not found")
	}
	apply(s)
	return nil
}

// pin adds a message to the chat's pins, newest last.
func (w *world) pin(chatID int64, messageID int) (models.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	c, ok := w.chats[chatID]
	if !ok {
		return models.Message{}, requestError("message to pin not found")
	}
	if err := c.mayPin(); err != nil {
		return models.Message{}, err
	}
	m := w.find(chatID, messageID)
	if m == nil {
		return models.Message{}, requestError("message to pin not found")
	}
	if !slices.Contains(c.pinned, messageID) {
		c.pinned = append(c.pinned, messageID)
	}
	return *m, nil
}

// unpin takes back the newest pin, or the one named. Telegram writes nothing in
// the chat for it, so nobody is told.
func (w *world) unpin(chatID int64, messageID int, all bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	c, ok := w.chats[chatID]
	if !ok {
		return requestError("chat not found")
	}
	if err := c.mayPin(); err != nil {
		return err
	}
	switch {
	case all:
		c.pinned = nil
	case messageID > 0:
		if i := slices.Index(c.pinned, messageID); i >= 0 {
			c.pinned = slices.Delete(c.pinned, i, i+1)
		}
	case len(c.pinned) > 0:
		c.pinned = c.pinned[:len(c.pinned)-1]
	}
	return nil
}

func (w *world) newestPin(chatID int64) (models.Message, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok || len(c.pinned) == 0 {
		return models.Message{}, false
	}
	m := w.find(chatID, c.pinned[len(c.pinned)-1])
	if m == nil {
		return models.Message{}, false
	}
	return *m, true
}

// migrate moves a group's people to a supergroup and leaves a forwarding
// address behind. The history stays: to a bot the supergroup is a new chat.
func (w *world) migrate(from, to int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	c, moved := w.chats[from], w.chats[to]
	if c == nil || moved == nil || c.movedTo != 0 {
		return false
	}
	c.movedTo = to
	moved.bot = c.bot
	maps.Copy(moved.members, c.members)
	return true
}

// moved reports the refusal a call to a chat that has since become a supergroup
// gets, carrying the id the bot should be using instead.
func (w *world) moved(chatID string) error {
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return nil
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[id]
	if !ok || c.movedTo == 0 {
		return nil
	}
	return &apiError{
		Code:            http.StatusBadRequest,
		Description:     "Bad Request: group chat was upgraded to a supergroup chat",
		MigrateToChatID: c.movedTo,
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
func (w *world) edit(chatID int64, messageID int, mutate func(*chat, *models.Message) error) (edited models.Message, found bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	m := w.find(chatID, messageID)
	if m == nil {
		return models.Message{}, false, nil
	}
	if err := mutate(w.chats[chatID], m); err != nil {
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
