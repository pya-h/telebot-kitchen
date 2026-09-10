package kitchen

import (
	"fmt"

	"github.com/go-telegram/bot/models"
)

type mediaKind struct {
	method string
	param  string
	put    func(*models.Message, File)
}

var mediaKinds = []mediaKind{
	{"sendPhoto", "photo", putPhoto},
	{"sendVoice", "voice", putVoice},
	{"sendAudio", "audio", putAudio},
	{"sendVideo", "video", putVideo},
	{"sendAnimation", "animation", putAnimation},
	{"sendDocument", "document", putDocument},
	{"sendSticker", "sticker", putSticker},
	{"sendVideoNote", "video_note", putVideoNote},
}

var editableKinds = map[string]func(*models.Message, File){}

func init() {
	for _, kind := range mediaKinds {
		apiMethods[kind.method] = kind.send
		switch kind.param {
		case "photo", "video", "animation", "audio", "document":
			editableKinds[kind.param] = kind.put
		}
	}
}

func (kind mediaKind) send(k *Kitchen, p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	file := p[kind.param]
	if file == "" {
		return nil, badRequest(kind.param)
	}
	// Read before accept, so markup the kitchen cannot read leaves the keyboard
	// already up alone.
	caption, entities, err := p.styled("caption", "caption_entities")
	if err != nil {
		return nil, err
	}
	markup, err := k.accept(p, chatID)
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	sent := models.Message{From: &sender, ReplyMarkup: markup}
	kind.put(&sent, k.files.fileOf(file))
	if _, captioned := mediaOf(&sent); captioned {
		sent.Caption, sent.CaptionEntities = caption, entities
	}
	return k.world.add(chatID, sent), nil
}

// mediaOf is what a client shows the message as, and whether Telegram lets it
// carry a caption: only the file kinds take one, and a venue is shown as itself
// rather than as the coordinates it also carries.
func mediaOf(m *models.Message) (label string, captioned bool) {
	switch {
	case len(m.Photo) > 0:
		return "photo", true
	case m.Voice != nil:
		return "voice", true
	case m.Audio != nil:
		return "audio", true
	case m.Video != nil:
		return "video", true
	case m.Animation != nil:
		return "animation", true
	case m.Document != nil:
		return "document", true
	case m.Sticker != nil:
		return "sticker", false
	case m.VideoNote != nil:
		return "video note", false
	case m.Venue != nil:
		return "venue", false
	case m.Location != nil:
		return "location", false
	case m.Contact != nil:
		return "contact", false
	case m.Dice != nil:
		return "dice", false
	case m.Poll != nil:
		return "poll", false
	}
	return "", false
}

func (k *Kitchen) carried(m *models.Message) string {
	label, _ := mediaOf(m)
	if label == "" {
		return ""
	}
	// A venue carries coordinates too, but a client shows it by its title.
	if m.Location != nil && m.Venue == nil {
		return fmt.Sprintf("%s %.4f, %.4f", label, m.Location.Latitude, m.Location.Longitude)
	}
	if file, known := k.files.get(fileIn(m)); known && file.Name != "" {
		return label + " " + file.Name
	}
	return label
}

func fileIn(m *models.Message) string {
	switch {
	case len(m.Photo) > 0:
		return m.Photo[len(m.Photo)-1].FileID
	case m.Voice != nil:
		return m.Voice.FileID
	case m.Audio != nil:
		return m.Audio.FileID
	case m.Video != nil:
		return m.Video.FileID
	case m.Animation != nil:
		return m.Animation.FileID
	case m.Document != nil:
		return m.Document.FileID
	case m.Sticker != nil:
		return m.Sticker.FileID
	case m.VideoNote != nil:
		return m.VideoNote.FileID
	}
	return ""
}

// Replacing media may change its kind, so the old one goes first.
func clearMedia(m *models.Message) {
	m.Photo, m.Voice, m.Audio, m.Video = nil, nil, nil, nil
	m.Animation, m.Document, m.Sticker, m.VideoNote = nil, nil, nil, nil
}

func putPhoto(m *models.Message, f File) { m.Photo = photoSizes(f) }

func putVoice(m *models.Message, f File) {
	m.Voice = &models.Voice{FileID: f.ID, FileUniqueID: f.UniqueID, FileSize: int64(len(f.Data))}
}

func putAudio(m *models.Message, f File) {
	m.Audio = &models.Audio{FileID: f.ID, FileUniqueID: f.UniqueID, FileName: f.Name, FileSize: int64(len(f.Data))}
}

func putVideo(m *models.Message, f File) {
	m.Video = &models.Video{FileID: f.ID, FileUniqueID: f.UniqueID, FileName: f.Name, FileSize: int64(len(f.Data))}
}

func putAnimation(m *models.Message, f File) {
	m.Animation = &models.Animation{FileID: f.ID, FileUniqueID: f.UniqueID, FileName: f.Name, FileSize: int64(len(f.Data))}
}

func putDocument(m *models.Message, f File) {
	m.Document = &models.Document{FileID: f.ID, FileUniqueID: f.UniqueID, FileName: f.Name, FileSize: int64(len(f.Data))}
}

func putSticker(m *models.Message, f File) {
	m.Sticker = &models.Sticker{FileID: f.ID, FileUniqueID: f.UniqueID, Type: "regular"}
}

func putVideoNote(m *models.Message, f File) {
	m.VideoNote = &models.VideoNote{FileID: f.ID, FileUniqueID: f.UniqueID, FileSize: len(f.Data)}
}
