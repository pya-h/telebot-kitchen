package kitchen

import (
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/go-telegram/bot/models"
)

// The reactions Telegram allows. A bot that reaches for anything else is
// refused live, so it is refused here.
var everyReaction = strings.Fields(
	`👍 👎 ❤ 🔥 🥰 👏 😁 🤔 🤯 😱 🤬 😢 🎉 🤩 🤮 💩 🙏 👌 🕊 🤡 🥱 🥴 😍 🐳 ❤‍🔥 🌚 🌭 💯
	 🤣 ⚡ 🍌 🏆 💔 🤨 😐 🍓 🍾 💋 🖕 😈 😴 😭 🤓 👻 👨‍💻 👀 🎃 🙈 😇 😨 🤝 ✍ 🤗 🫡 🎅 🎄
	 ☃ 💅 🤪 🗿 🆒 💘 🙉 🦄 😘 💊 🙊 😎 👾 🤷‍♂ 🤷 🤷‍♀ 😡`)

func plainEmoji(emoji string) string { return strings.ReplaceAll(emoji, "️", "") }

func reactable(emoji string) bool {
	return slices.ContainsFunc(everyReaction, func(allowed string) bool {
		return plainEmoji(allowed) == plainEmoji(emoji)
	})
}

type reactionKey struct {
	chatID    int64
	messageID int
}

type reactionBook struct {
	mu sync.Mutex
	by map[reactionKey]map[int64][]string
}

func newReactionBook() *reactionBook {
	return &reactionBook{by: map[reactionKey]map[int64][]string{}}
}

func (b *reactionBook) set(chatID int64, messageID int, actor int64, emoji []string) (was []string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	at := reactionKey{chatID, messageID}
	on, reacted := b.by[at]
	if !reacted {
		on = map[int64][]string{}
		b.by[at] = on
	}
	was = on[actor]
	if len(emoji) == 0 {
		delete(on, actor)
	} else {
		on[actor] = slices.Clone(emoji)
	}
	return was
}

func (b *reactionBook) tally(chatID int64, messageID int) ([]string, map[string]int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	on := b.by[reactionKey{chatID, messageID}]
	if len(on) == 0 {
		return nil, nil
	}
	counts := map[string]int{}
	var order []string
	for _, actor := range slices.Sorted(maps.Keys(on)) {
		for _, emoji := range on[actor] {
			if counts[emoji] == 0 {
				order = append(order, emoji)
			}
			counts[emoji]++
		}
	}
	return order, counts
}

func (b *reactionBook) on(chatID int64, messageID int) []string {
	order, _ := b.tally(chatID, messageID)
	return order
}

func reactionTypes(emoji []string) []models.ReactionType {
	types := make([]models.ReactionType, len(emoji))
	for i, e := range emoji {
		types[i] = models.ReactionType{
			Type:              models.ReactionTypeTypeEmoji,
			ReactionTypeEmoji: &models.ReactionTypeEmoji{Type: models.ReactionTypeTypeEmoji, Emoji: e},
		}
	}
	return types
}

// A bot reacting to a message is not told it did, the way none of its own doing
// reaches it.
func (k *Kitchen) setMessageReaction(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	messageID, err := p.messageID()
	if err != nil {
		return nil, err
	}

	if err := k.world.reach(chatID); err != nil {
		return nil, err
	}

	var wanted []models.ReactionType
	if err := p.decode("reaction", &wanted); err != nil {
		return nil, badRequest("reaction")
	}
	emoji := make([]string, 0, len(wanted))
	for _, reaction := range wanted {
		if reaction.Type != models.ReactionTypeTypeEmoji {
			return nil, requestError("REACTION_INVALID")
		}
		if !reactable(reaction.ReactionTypeEmoji.Emoji) {
			return nil, requestError("REACTION_INVALID")
		}
		emoji = append(emoji, reaction.ReactionTypeEmoji.Emoji)
	}
	if _, found := k.world.message(chatID, messageID); !found {
		return nil, requestError("message to react to not found")
	}

	k.reactions.set(chatID, messageID, k.botUser().ID, emoji)
	return true, nil
}

func (m *Member) React(emoji ...string) {
	sent, said := m.kitchen().world.latest(m.chat.id)
	if !said {
		m.kitchen().tb.Errorf("kitchen: %s has nothing to react to", m)
		return
	}
	m.reactTo(sent.ID, emoji)
}

func (m *Member) ReactTo(sent Message, emoji ...string) { m.reactTo(sent.ID, emoji) }

func (m *Member) reactTo(messageID int, emoji []string) {
	k := m.kitchen()
	if m.shutOut() {
		return
	}
	for _, e := range emoji {
		if !reactable(e) {
			k.tb.Errorf("kitchen: Telegram takes no %q as a reaction", e)
			return
		}
	}
	if _, found := k.world.message(m.chat.id, messageID); !found {
		k.tb.Errorf("kitchen: %s has no message %d to react to", m, messageID)
		return
	}

	m.awaitFromNow()
	was := k.reactions.set(m.chat.id, messageID, m.user.id, emoji)
	k.tellAboutReaction(m, messageID, was, emoji)
}

// Who hears about a reaction is Telegram's rule: a channel counts them without
// naming anybody, a private chat names the one person there, and a group tells
// only a bot that administers it.
func (k *Kitchen) tellAboutReaction(m *Member, messageID int, was, now []string) {
	info, known := k.world.info(m.chat.id)
	if !known {
		return
	}
	when := int(k.clock.Now().Unix())

	if m.chat.kind == models.ChatTypeChannel {
		order, counts := k.reactions.tally(m.chat.id, messageID)
		totals := make([]models.ReactionCount, len(order))
		for i, emoji := range order {
			totals[i] = models.ReactionCount{Type: reactionTypes([]string{emoji})[0], TotalCount: counts[emoji]}
		}
		k.deliver(models.Update{MessageReactionCount: &models.MessageReactionCountUpdated{
			Chat: info, MessageID: messageID, Date: when, Reactions: totals,
		}})
		return
	}
	if m.chat.kind != models.ChatTypePrivate && !k.world.botAdministers(m.chat.id) {
		return
	}

	who := m.user.identity()
	k.deliver(models.Update{MessageReaction: &models.MessageReactionUpdated{
		Chat: info, MessageID: messageID, User: &who, Date: when,
		OldReaction: reactionTypes(was), NewReaction: reactionTypes(now),
	}})
}
