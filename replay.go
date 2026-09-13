package kitchen

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/go-telegram/bot/models"
)

// Replay hands a recorded session's updates to the bot under test, in the order
// they arrived, so an incident in production becomes an ordinary test.
func (k *Kitchen) Replay(path string) {
	file, err := os.Open(path)
	if err != nil {
		k.tb.Errorf("kitchen: replay %s: %v", path, err)
		return
	}
	defer file.Close()
	k.ReplayFrom(file)
}

func (k *Kitchen) ReplayFrom(r io.Reader) {
	recorded, err := recordedIn(r)
	if err != nil {
		k.tb.Errorf("kitchen: read the recording: %v", err)
		return
	}
	if len(recorded) == 0 {
		k.tb.Errorf("kitchen: the recording holds no updates")
		return
	}

	// The whole cast is seated before the first update lands, so a bot that
	// asks about somebody who only speaks later is still answered.
	k.allowRecorded(recorded)
	was := botBehind(recorded)
	for _, u := range recorded {
		k.seat(u, was)
	}
	for _, u := range recorded {
		k.deliver(u)
	}
}

// allowRecorded adds what the recording carries to the kinds the bot hears: a
// recording is of a bot that was receiving them.
func (k *Kitchen) allowRecorded(recorded []models.Update) {
	k.mu.Lock()
	defer k.mu.Unlock()

	asked := slices.Clone(k.allowed)
	if len(asked) == 0 {
		asked = DefaultUpdates()
	}
	for _, u := range recorded {
		if kind := kindOf(&u); kind != "" && !slices.Contains(asked, kind) {
			asked = append(asked, kind)
		}
	}
	k.allowed = asked
}

// botBehind is the bot the recording was made by, taken from the first message
func botBehind(recorded []models.Update) int64 {
	for _, u := range recorded {
		if _, _, carried := about(&u); carried != nil && carried.From != nil && carried.From.IsBot {
			return carried.From.ID
		}
	}
	return 0
}

func recordedIn(r io.Reader) ([]models.Update, error) {
	var recorded []models.Update
	lines := bufio.NewScanner(r)
	// An update carrying an album of captions outgrows the scanner's default.
	lines.Buffer(nil, 4<<20)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if line == "" {
			continue
		}
		var u models.Update
		if err := json.Unmarshal([]byte(line), &u); err != nil {
			return nil, err
		}
		recorded = append(recorded, u)
	}
	return recorded, lines.Err()
}

func (k *Kitchen) seat(u models.Update, was int64) {
	where, who, carried := about(&u)
	if where != nil {
		k.world.describe(*where)
	}
	if who != nil && !who.IsBot {
		person := k.User(who.ID, named(who)...)
		if where != nil {
			k.world.join(where.ID, person.identity())
		}
	}
	if carried != nil {
		k.files.recall(carried)
	}
	if where != nil && carried != nil {
		k.world.restore(where.ID, k.assigned(*carried, was))
	}
}

func named(who *models.User) []UserOption {
	var opts []UserOption
	if who.FirstName != "" {
		opts = append(opts, WithFullName(who.FirstName, who.LastName))
	}
	if who.Username != "" {
		opts = append(opts, WithUsername(who.Username))
	}
	return opts
}

func (k *Kitchen) assigned(m models.Message, was int64) models.Message {
	ours := k.botUser()
	if m.From != nil && m.From.ID == was {
		m.From = &ours
	}
	if m.ViaBot != nil && m.ViaBot.ID == was {
		m.ViaBot = &ours
	}
	return m
}

// about is what an update is about: the chat it happened in, whose doing it was, etc
func about(u *models.Update) (where *models.Chat, who *models.User, carried *models.Message) {
	switch {
	case u.Message != nil:
		return &u.Message.Chat, u.Message.From, u.Message
	case u.EditedMessage != nil:
		return &u.EditedMessage.Chat, u.EditedMessage.From, u.EditedMessage
	case u.ChannelPost != nil:
		return &u.ChannelPost.Chat, nil, u.ChannelPost
	case u.EditedChannelPost != nil:
		return &u.EditedChannelPost.Chat, nil, u.EditedChannelPost

	case u.CallbackQuery != nil:
		// The tap names the screen it was made on, which has to be there for the
		// bot to answer by editing it.
		if tapped := u.CallbackQuery.Message.Message; tapped != nil {
			return &tapped.Chat, &u.CallbackQuery.From, tapped
		}
		return nil, &u.CallbackQuery.From, nil

	case u.MyChatMember != nil:
		return &u.MyChatMember.Chat, &u.MyChatMember.From, nil
	case u.ChatMember != nil:
		return &u.ChatMember.Chat, &u.ChatMember.From, nil
	case u.ChatJoinRequest != nil:
		return &u.ChatJoinRequest.Chat, &u.ChatJoinRequest.From, nil
	case u.MessageReaction != nil:
		return &u.MessageReaction.Chat, u.MessageReaction.User, nil
	case u.MessageReactionCount != nil:
		return &u.MessageReactionCount.Chat, nil, nil
	case u.ChatBoost != nil:
		return &u.ChatBoost.Chat, nil, nil
	case u.RemovedChatBoost != nil:
		return &u.RemovedChatBoost.Chat, nil, nil

	// The rest happen to the bot rather than in a chat.
	case u.InlineQuery != nil:
		return nil, u.InlineQuery.From, nil
	case u.ChosenInlineResult != nil:
		return nil, &u.ChosenInlineResult.From, nil
	case u.PollAnswer != nil:
		return nil, u.PollAnswer.User, nil
	case u.PreCheckoutQuery != nil:
		return nil, u.PreCheckoutQuery.From, nil
	}
	return nil, nil, nil
}
