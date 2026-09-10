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
	bot     standing
	members map[int64]*standing
	pinned  []int
	// A reply keyboard belongs to the chat, not to the message that raised it:
	// it stays up until another one replaces or removes it.
	menu          [][]string
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
	nextAlbumID  atomic.Int64
	nextPollID   atomic.Int64
	nextRollID   atomic.Int64
}

func newWorld(clock *Clock, bot models.User) *world {
	return &world{clock: clock, bot: bot, chats: map[int64]*chat{}}
}

func (w *world) nextUpdate() int64 { return w.nextUpdateID.Add(1) }

func (w *world) nextQuery() string { return "query-" + strconv.FormatInt(w.nextQueryID.Add(1), 10) }

func (w *world) nextAlbum() string { return "album-" + strconv.FormatInt(w.nextAlbumID.Add(1), 10) }

func (w *world) nextPoll() string { return "poll-" + strconv.FormatInt(w.nextPollID.Add(1), 10) }

// A roll has to be repeatable to be worth asserting, so the faces come up in
// turn rather than at random.
func (w *world) nextRoll(faces int) int { return int(w.nextRollID.Add(1)-1)%faces + 1 }

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

// register describes a chat, handing back the kind it ends up with: an id
// already taken keeps the one it was first given.
func (w *world) register(id int64, kind models.ChatType, title string, bot models.User) (models.ChatType, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if c, ok := w.chats[id]; ok && c.info.Type != kind {
		return c.info.Type, false
	}
	w.chatOf(id, kind, title, bot)
	return kind, true
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
	admins := make([]*models.ChatMember, 0, len(c.members)+1)
	add := func(s *standing) {
		if s.status == models.ChatMemberTypeAdministrator || s.status == models.ChatMemberTypeOwner {
			member := s.chatMember()
			admins = append(admins, &member)
		}
	}
	add(&c.bot)
	for _, id := range slices.Sorted(maps.Keys(c.members)) {
		add(c.members[id])
	}
	return admins
}

// botAdministers gates who hears about a reaction: Telegram tells a bot about
// reactions in a group only while it administers one.
func (w *world) botAdministers(chatID int64) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return false
	}
	return c.bot.status == models.ChatMemberTypeAdministrator || c.bot.status == models.ChatMemberTypeOwner
}

func (w *world) botPresent(chatID int64) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	return ok && c.bot.present()
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
	if s := w.place(c, u); s.status == models.ChatMemberTypeLeft {
		s.status = models.ChatMemberTypeMember
	}
	return true
}

// manage changes a member's standing on the bot's say-so, if it may.
// mayManage is the rights check on its own, for the verbs that change nobody's
// standing directly.
func (w *world) mayManage(chatID int64, need Right, what string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return requestError("chat not found")
	}
	return c.mayManage(need, what)
}

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
	return handed(m), nil
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
	return handed(m), true
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
	moved.menu = c.menu
	for id, s := range c.members {
		carried := *s
		moved.members[id] = &carried
	}
	return true
}

func (w *world) migratedTo(chatID int64) (int64, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok || c.movedTo == 0 {
		return 0, false
	}
	return c.movedTo, true
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

func (w *world) setMenu(chatID int64, rows [][]string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.chatAt(chatID).menu = rows
}

func (w *world) menu(chatID int64) [][]string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return nil
	}
	rows := make([][]string, len(c.menu))
	for i, row := range c.menu {
		rows[i] = slices.Clone(row)
	}
	return rows
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
	if c.info.Type == models.ChatTypeChannel {
		posted := c.info
		m.SenderChat = &posted
	}
	c.nextMessageID++

	c.messages = append(c.messages, &m)
	return handed(&m)
}

// describe registers a chat from a recording, which knows more about it than a
// test standing one up by hand does.
func (w *world) describe(info models.Chat) {
	w.mu.Lock()
	defer w.mu.Unlock()

	c := w.chatOf(info.ID, info.Type, info.Title, w.bot)
	if info.Username != "" {
		c.info.Username = info.Username
	}
	if info.FirstName != "" {
		c.info.FirstName, c.info.LastName = info.FirstName, info.LastName
	}
}

// restore puts a recorded message back at the id it had
func (w *world) restore(chatID int64, m models.Message) {
	w.mu.Lock()
	defer w.mu.Unlock()

	c := w.chatAt(chatID)
	m.Chat = c.info
	// Last seen wins: a message the recording went on to edit ends where it ended.
	if already := w.find(chatID, m.ID); already != nil {
		*already = m
		return
	}
	at := len(c.messages)
	for at > 0 && c.messages[at-1].ID > m.ID {
		at--
	}
	c.messages = slices.Insert(c.messages, at, &m)
	if m.ID >= c.nextMessageID {
		c.nextMessageID = m.ID + 1
	}
}

func (w *world) message(chatID int64, messageID int) (models.Message, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	m := w.find(chatID, messageID)
	if m == nil {
		return models.Message{}, false
	}
	return handed(m), true
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

func (w *world) onPoll(chatID int64, messageID int, change func(*models.Poll)) (models.Poll, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	m := w.find(chatID, messageID)
	if m == nil || m.Poll == nil {
		return models.Poll{}, false
	}
	change(m.Poll)
	return copyPoll(m.Poll), true
}

func (w *world) newestPoll(chatID int64) (models.Poll, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return models.Poll{}, false
	}
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Poll != nil {
			return copyPoll(c.messages[i].Poll), true
		}
	}
	return models.Poll{}, false
}

func copyPoll(poll *models.Poll) models.Poll {
	got := *poll
	got.Options = slices.Clone(poll.Options)
	return got
}

// handed is a message on its way out of the world. A poll is the one thing a
// stored message keeps changing in place, so a copy leaves carrying its own:
// otherwise the next vote rewrites the tally under whoever was handed it.
func handed(m *models.Message) models.Message {
	out := *m
	if m.Poll != nil {
		poll := copyPoll(m.Poll)
		out.Poll = &poll
	}
	return out
}

func (w *world) edit(chatID int64, messageID int, mutate func(*chat, *models.Message) error) (edited models.Message, found bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	m := w.find(chatID, messageID)
	if m == nil {
		return models.Message{}, false, nil
	}
	if err := mutate(w.chats[chatID], m); err != nil {
		return handed(m), true, err
	}
	m.EditDate = int(w.clock.Now().Unix())
	return handed(m), true, nil
}

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
		c.pinned = slices.DeleteFunc(c.pinned, func(id int) bool { return id == messageID })
		return true, nil
	}
	return false, nil
}

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
		screens = append(screens, handed(c.messages[i]))
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
	return handed(c.messages[len(c.messages)-1]), true
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
		log[i] = handed(m)
	}
	return log
}
