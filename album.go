package kitchen

import (
	"fmt"
	"slices"

	"github.com/go-telegram/bot/models"
)

// Telegram album limits
const (
	minAlbum = 2
	maxAlbum = 10
)

// Attachment is one file in an album, made by Photo, Video, Audio or Document —
// the four kinds Telegram groups.
type Attachment struct {
	kind    string
	name    string
	data    []byte
	caption string
}

func Photo(name string, data []byte, caption string) Attachment {
	return Attachment{"photo", name, data, caption}
}

func Video(name string, data []byte, caption string) Attachment {
	return Attachment{"video", name, data, caption}
}

func Audio(name string, data []byte, caption string) Attachment {
	return Attachment{"audio", name, data, caption}
}

func Document(name string, data []byte, caption string) Attachment {
	return Attachment{"document", name, data, caption}
}

// grouped says why these kinds cannot travel together, and "" when they can.
// Photos and videos mix; an album of documents or of audio stands alone.
func grouped(kinds []string) string {
	if n := len(kinds); n < minAlbum || n > maxAlbum {
		return fmt.Sprintf("wrong number of media: must be between %d and %d", minAlbum, maxAlbum)
	}
	for _, kind := range kinds {
		if _, groupable := albumKinds[kind]; !groupable {
			return "a " + kind + " cannot travel in a media group"
		}
	}
	for _, alone := range []string{"document", "audio"} {
		if slices.Contains(kinds, alone) && slices.ContainsFunc(kinds, func(k string) bool { return k != alone }) {
			return alone + " must be the only kind in a media group"
		}
	}
	return ""
}

// The kinds Telegram has an InputMedia for that may travel in a group: an
// animation, a sticker, a voice note and a video note always go alone.
var albumKinds = map[string]func(*models.Message, File){}

func init() {
	for _, kind := range mediaKinds {
		switch kind.param {
		case "photo", "video", "audio", "document":
			albumKinds[kind.param] = kind.put
		}
	}
	apiMethods["sendMediaGroup"] = (*Kitchen).sendMediaGroup
}

func (k *Kitchen) sendMediaGroup(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	var group []struct {
		Type            string                 `json:"type"`
		Media           string                 `json:"media"`
		Caption         string                 `json:"caption"`
		ParseMode       string                 `json:"parse_mode"`
		CaptionEntities []models.MessageEntity `json:"caption_entities"`
	}
	if err := p.decode("media", &group); err != nil {
		return nil, badRequest("media")
	}

	// Every caption is read before any of the album lands, so markup the kitchen
	// cannot read leaves no half-sent group behind.
	kinds := make([]string, len(group))
	for i := range group {
		kinds[i] = group[i].Type
		caption, marked, err := fitCaption(styledText(group[i].Caption, group[i].ParseMode, group[i].CaptionEntities))
		if err != nil {
			return nil, err
		}
		group[i].Caption, group[i].CaptionEntities = caption, marked
	}
	if why := grouped(kinds); why != "" {
		return nil, requestError(why)
	}
	if err := k.mayPost(chatID); err != nil {
		return nil, err
	}
	files := make([]File, len(group))
	for i, item := range group {
		if files[i], err = k.files.resolve(p.attached(item.Media), item.Type); err != nil {
			return nil, err
		}
	}

	sender := k.botUser()
	album := k.world.nextAlbum()
	sent := make([]models.Message, len(group))
	for i, item := range group {
		message := models.Message{
			From: &sender, MediaGroupID: album,
			Caption: item.Caption, CaptionEntities: item.CaptionEntities,
		}
		albumKinds[item.Type](&message, files[i])
		sent[i] = k.world.add(chatID, message)
	}
	return sent, nil
}

func (m *Member) SendAlbum(files ...Attachment) {
	kinds := make([]string, len(files))
	for i, file := range files {
		kinds[i] = file.kind
	}
	if why := grouped(kinds); why != "" {
		m.kitchen().tb.Errorf("kitchen: %s cannot send that album: %s", m, why)
		return
	}

	album := m.kitchen().world.nextAlbum()
	messages := make([]models.Message, len(files))
	for i, file := range files {
		messages[i] = models.Message{MediaGroupID: album, Caption: file.caption}
		albumKinds[file.kind](&messages[i], m.kitchen().files.issue(file.kind, file.name, file.data))
	}
	m.sayAll(messages...)
}
