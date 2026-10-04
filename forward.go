package kitchen

import (
	"errors"
	"fmt"

	"github.com/go-telegram/bot/models"
)

// Origin is who a forward is credited to, for content the test hands over
// without staging the message it came from.
type Origin struct {
	user      *User
	name      string
	chat      *Chat
	post      bool
	messageID int
	signature string
}

func FromUser(u *User) Origin { return Origin{user: u} }

func FromHiddenUser(name string) Origin { return Origin{name: name} }

// FromChat is a group or supergroup an admin spoke for anonymously.
func FromChat(c *Chat, signature string) Origin { return Origin{chat: c, signature: signature} }

func FromChannel(c *Chat, messageID int, signature string) Origin {
	return Origin{chat: c, post: true, messageID: messageID, signature: signature}
}

// The original is dated now, as the kitchen holds no earlier message to date it by.
func (o Origin) resolve(k *Kitchen) (*models.MessageOrigin, error) {
	date := int(k.clock.Now().Unix())
	switch {
	case o.user != nil:
		author := o.user.identity()
		return k.originOf(models.Message{Date: date, From: &author}), nil

	case o.chat != nil:
		info, _ := k.world.info(o.chat.id)
		channel := info.Type == models.ChatTypeChannel
		switch {
		case o.post && !channel:
			return nil, fmt.Errorf("%q is a %s, and only a channel posts", info.Title, info.Type)
		case !o.post && channel:
			return nil, fmt.Errorf("%q is a channel, whose posts are forwarded FromChannel", info.Title)
		case o.post && o.messageID <= 0:
			return nil, fmt.Errorf("a post in %q needs its message id, not %d", info.Title, o.messageID)
		case o.post:
			return &models.MessageOrigin{
				Type: models.MessageOriginTypeChannel,
				MessageOriginChannel: &models.MessageOriginChannel{
					Date: date, Chat: info, MessageID: o.messageID, AuthorSignature: signature(o.signature),
				},
			}, nil
		}
		return &models.MessageOrigin{
			Type: models.MessageOriginTypeChat,
			MessageOriginChat: &models.MessageOriginChat{
				Date: date, SenderChat: info, AuthorSignature: signature(o.signature),
			},
		}, nil

	case o.name != "":
		return &models.MessageOrigin{
			Type:                    models.MessageOriginTypeHiddenUser,
			MessageOriginHiddenUser: &models.MessageOriginHiddenUser{Date: date, SenderUserName: o.name},
		}, nil
	}
	return nil, errors.New("it is credited to nobody: build its origin with FromUser, FromHiddenUser, FromChat or FromChannel")
}

// Forward hands the bot messages already in the kitchen, as a client forwards
// what it selected: several parts of one album together stay an album.
func (m *Member) Forward(msgs ...Message) {
	k := m.kitchen()
	if len(msgs) == 0 {
		k.tb.Errorf("kitchen: %s has nothing to forward", m)
		return
	}

	sources := make([]models.Message, len(msgs))
	parts := map[string]int{}
	for i, msg := range msgs {
		source, found := k.world.message(msg.ChatID, msg.ID)
		label, _ := mediaOf(&source)
		switch {
		case !found:
			k.tb.Errorf("kitchen: %s cannot forward message %d of chat %d, which is not there", m, msg.ID, msg.ChatID)
			return
		case source.Chat.Type == models.ChatTypePrivate && source.Chat.ID != m.user.id:
			k.tb.Errorf("kitchen: %s cannot forward message %d, which is in somebody else's private chat", m, msg.ID)
			return
		case source.Poll != nil:
			k.tb.Errorf("kitchen: %s cannot forward a poll, which the kitchen does not model", m)
			return
		case source.Text == "" && label == "":
			k.tb.Errorf("kitchen: %s cannot forward message %d, a service message Telegram does not forward", m, msg.ID)
			return
		}
		sources[i] = source
		if source.MediaGroupID != "" {
			parts[source.MediaGroupID]++
		}
	}

	// Telegram gives the parts a new album of their own, and a lone part none.
	regrouped := map[string]string{}
	forwarded := make([]models.Message, len(sources))
	for i, source := range sources {
		forwarded[i] = forwardedAs(source, k.originOf(source))
		if parts[source.MediaGroupID] < minAlbum {
			continue
		}
		if regrouped[source.MediaGroupID] == "" {
			regrouped[source.MediaGroupID] = k.world.nextAlbum()
		}
		forwarded[i].MediaGroupID = regrouped[source.MediaGroupID]
	}
	m.sayAll(forwarded...)
}

func (m *Member) ForwardText(from Origin, text Formatted) {
	origin, ok := m.credited(from)
	if !ok {
		return
	}
	words, entities, err := text.resolve()
	switch {
	case err != nil:
		m.kitchen().tb.Errorf("kitchen: %s cannot forward that: %v", m, err)
		return
	case words == "":
		m.kitchen().tb.Errorf("kitchen: %s cannot forward an empty message", m)
		return
	}
	m.forwardAs(origin, models.Message{Text: words, Entities: withCommand(words, entities)})
}

func (m *Member) ForwardFile(from Origin, file Attachment) {
	origin, ok := m.credited(from)
	if !ok {
		return
	}
	if sent, ok := m.attached(file); ok {
		m.forwardAs(origin, sent)
	}
}

func (m *Member) ForwardAlbum(from Origin, files ...Attachment) {
	origin, ok := m.credited(from)
	if !ok {
		return
	}
	if album, ok := m.album(files); ok {
		m.forwardAs(origin, album...)
	}
}

func (m *Member) ForwardLocation(from Origin, latitude, longitude float64) {
	if origin, ok := m.credited(from); ok {
		m.forwardAs(origin, models.Message{Location: &models.Location{Latitude: latitude, Longitude: longitude}})
	}
}

func (m *Member) ForwardVenue(from Origin, latitude, longitude float64, title, address string) {
	origin, ok := m.credited(from)
	if !ok {
		return
	}
	where := models.Location{Latitude: latitude, Longitude: longitude}
	m.forwardAs(origin, models.Message{
		Location: &where,
		Venue:    &models.Venue{Location: where, Title: title, Address: address},
	})
}

func (m *Member) credited(from Origin) (*models.MessageOrigin, bool) {
	origin, err := from.resolve(m.kitchen())
	if err != nil {
		m.kitchen().tb.Errorf("kitchen: %s cannot forward that: %v", m, err)
		return nil, false
	}
	return origin, true
}

func (m *Member) forwardAs(origin *models.MessageOrigin, msgs ...models.Message) {
	for i := range msgs {
		msgs[i].ForwardOrigin = origin
	}
	m.sayAll(msgs...)
}
