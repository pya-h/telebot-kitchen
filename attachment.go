package kitchen

import "github.com/go-telegram/bot/models"

// A media kind is one Bot API send method: the parameter it carries its file
// in, and where that file lands on the message it produces.
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

func init() {
	for _, kind := range mediaKinds {
		apiMethods[kind.method] = kind.send
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
	markup, err := k.accept(p, chatID)
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	sent := models.Message{From: &sender, ReplyMarkup: markup}
	kind.put(&sent, k.files.fileOf(file))
	if _, captioned := mediaOf(&sent); captioned {
		sent.Caption = p["caption"]
	}
	return k.world.add(chatID, sent), nil
}

// mediaOf is what a client shows the message as, and whether Telegram lets it
// carry a caption: a sticker, a video note and a location take none.
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
	case m.Location != nil:
		return "location", false
	}
	return "", false
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
