package kitchen

import (
	"fmt"

	"github.com/go-telegram/bot/models"
)

// Chat is a group or channel: one people share, rather than a user's own.
type Chat struct {
	kitchen *Kitchen
	id      int64
	kind    models.ChatType
}

// Group returns the group with this id, creating it on first mention.
func (k *Kitchen) Group(id int64, title string) *Chat {
	return k.sharedChat(id, models.ChatTypeGroup, title)
}

func (k *Kitchen) Supergroup(id int64, title string) *Chat {
	return k.sharedChat(id, models.ChatTypeSupergroup, title)
}

func (k *Kitchen) Channel(id int64, title string) *Chat {
	return k.sharedChat(id, models.ChatTypeChannel, title)
}

func (k *Kitchen) sharedChat(id int64, kind models.ChatType, title string) *Chat {
	// Bots branch on the sign, so a positive id would test a chat that cannot exist.
	if id >= 0 {
		k.tb.Errorf("kitchen: a %s id must be negative, as Telegram's are, not %d", kind, id)
	}
	if title == "" {
		title = fmt.Sprintf("Chat%d", -id)
	}
	k.world.register(id, kind, title, k.botUser())
	return &Chat{kitchen: k, id: id, kind: kind}
}

func (c *Chat) ID() int64 { return c.id }

func (c *Chat) Title() string { return c.kitchen.world.title(c.id) }

// Members lists who is in the chat, in id order.
func (c *Chat) Members() []*User {
	ids := c.kitchen.world.roster(c.id)
	members := make([]*User, len(ids))
	for i, id := range ids {
		members[i] = c.kitchen.User(id)
	}
	return members
}

func (c *Chat) History() []Message { return c.kitchen.History(c.id) }

func (c *Chat) Transcript() string { return c.kitchen.Transcript(c.id) }

// Post publishes as the channel itself, the way an admin posting from a client
// does. What the bot sends through the API does not come back to it as a post.
func (c *Chat) Post(text string) Message {
	if c.kind != models.ChatTypeChannel {
		c.kitchen.tb.Errorf("kitchen: %q is a %s, and only a channel carries posts", c.Title(), c.kind)
		return Message{}
	}

	sent := c.kitchen.world.add(c.id, models.Message{Text: text, Entities: commandEntities(text)})
	c.kitchen.deliver(models.Update{ChannelPost: &sent})
	return c.kitchen.view(sent)
}

// EditPost is the post reworded from a client; an edit the bot makes is its own.
func (c *Chat) EditPost(post Message, text string) Message {
	if c.kind != models.ChatTypeChannel {
		c.kitchen.tb.Errorf("kitchen: %q is a %s, and only a channel carries posts", c.Title(), c.kind)
		return Message{}
	}

	edited, found, _ := c.kitchen.world.edit(c.id, post.ID, func(m *models.Message) error {
		m.Text, m.Entities = text, commandEntities(text)
		return nil
	})
	if !found {
		c.kitchen.tb.Errorf("kitchen: %q has no post %d to edit", c.Title(), post.ID)
		return Message{}
	}
	c.kitchen.deliver(models.Update{EditedChannelPost: &edited})
	return c.kitchen.view(edited)
}
