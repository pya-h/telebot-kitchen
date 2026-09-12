package kitchen

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/go-telegram/bot/models"
)

// Telegram takes at most this many results in one answer.
const mostResults = 50

type inlineMedia struct {
	kind    string
	cached  string
	fetched string
}

var inlineKinds = map[string]inlineMedia{
	"photo":     {"photo", "photo_file_id", "photo_url"},
	"gif":       {"animation", "gif_file_id", "gif_url"},
	"mpeg4_gif": {"animation", "mpeg4_file_id", "mpeg4_url"},
	"video":     {"video", "video_file_id", "video_url"},
	"audio":     {"audio", "audio_file_id", "audio_url"},
	"voice":     {"voice", "voice_file_id", "voice_url"},
	"document":  {"document", "document_file_id", "document_url"},
	"sticker":   {"sticker", "sticker_file_id", ""},
}

// inlineResult is a result as it arrives on the wire. The library models these
// as an interface, and the kitchen's contract is the JSON either way.
type inlineResult struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Caption string `json:"caption"`

	Content     *inlineContent               `json:"input_message_content"`
	ReplyMarkup *models.InlineKeyboardMarkup `json:"reply_markup"`

	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Address     string  `json:"address"`
	PhoneNumber string  `json:"phone_number"`
	FirstName   string  `json:"first_name"`
	LastName    string  `json:"last_name"`
}

type inlineContent struct {
	MessageText string  `json:"message_text"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Title       string  `json:"title"`
	Address     string  `json:"address"`
	PhoneNumber string  `json:"phone_number"`
	FirstName   string  `json:"first_name"`
	LastName    string  `json:"last_name"`
}

type offer struct {
	id      string
	title   string
	message models.Message
}

type inlineBook struct {
	mu    sync.Mutex
	asked map[string]*inlineQuestion
	count int
}

type inlineQuestion struct {
	at       int // when it was asked, since the ids sort as text and "9" outranks "10"
	chatID   int64
	userID   int64
	query    string
	answered bool
	offers   []offer
}

func newInlineBook() *inlineBook { return &inlineBook{asked: map[string]*inlineQuestion{}} }

func (b *inlineBook) ask(id string, chatID, userID int64, query string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.count++
	b.asked[id] = &inlineQuestion{at: b.count, chatID: chatID, userID: userID, query: query}
}

// answer fills in a question once; a second answer is as late as no question.
func (b *inlineBook) answer(id string, offers []offer) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	asked, known := b.asked[id]
	if !known || asked.answered {
		return false
	}
	asked.answered, asked.offers = true, offers
	return true
}

// newest is the answered question a member picks from, since a test picks from what they last searched for.
func (b *inlineBook) newest(chatID, userID int64) (inlineQuestion, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var found *inlineQuestion
	for _, asked := range b.asked {
		if asked.chatID != chatID || asked.userID != userID || !asked.answered {
			continue
		}
		if found == nil || asked.at > found.at {
			found = asked
		}
	}
	if found == nil {
		return inlineQuestion{}, false
	}
	return *found, true
}

func (k *Kitchen) answerInlineQuery(p params) (any, error) {
	id := p["inline_query_id"]
	if id == "" {
		return nil, badRequest("inline_query_id")
	}

	var raw []json.RawMessage
	if err := p.decode("results", &raw); err != nil {
		return nil, badRequest("results")
	}
	if len(raw) > mostResults {
		return nil, requestError("RESULTS_TOO_MUCH")
	}

	offers := make([]offer, 0, len(raw))
	seen := map[string]bool{}
	for _, one := range raw {
		var result inlineResult
		if err := json.Unmarshal(one, &result); err != nil {
			return nil, badRequest("results")
		}
		if result.ID == "" {
			return nil, badRequest("results")
		}
		if seen[result.ID] {
			return nil, requestError("RESULT_ID_DUPLICATE")
		}
		seen[result.ID] = true

		var fields map[string]any
		if err := json.Unmarshal(one, &fields); err != nil {
			return nil, badRequest("results")
		}
		var taps struct {
			ReplyMarkup buttonData `json:"reply_markup"`
		}
		if err := json.Unmarshal(one, &taps); err != nil {
			return nil, badRequest("results")
		}
		if err := taps.ReplyMarkup.check(); err != nil {
			return nil, err
		}
		message, err := k.becomes(result, fields)
		if err != nil {
			return nil, err
		}
		offers = append(offers, offer{id: result.ID, title: result.Title, message: message})
	}

	// An answer to a query nobody is waiting on is how Telegram spells one that arrived too late.
	if !k.inline.answer(id, offers) {
		return nil, requestError("query is too old and response timeout expired or query ID is invalid")
	}
	return true, nil
}

// becomes is the message a result turns into when somebody picks it.
func (k *Kitchen) becomes(result inlineResult, fields map[string]any) (models.Message, error) {
	msg := models.Message{ReplyMarkup: result.ReplyMarkup}

	if c := result.Content; c != nil {
		switch {
		case c.MessageText != "":
			msg.Text = c.MessageText
		case c.PhoneNumber != "":
			msg.Contact = &models.Contact{PhoneNumber: c.PhoneNumber, FirstName: c.FirstName, LastName: c.LastName}
		case c.Address != "":
			where := models.Location{Latitude: c.Latitude, Longitude: c.Longitude}
			msg.Location, msg.Venue = &where, &models.Venue{Location: where, Title: c.Title, Address: c.Address}
		case c.Latitude != 0 || c.Longitude != 0:
			msg.Location = &models.Location{Latitude: c.Latitude, Longitude: c.Longitude}
		default:
			return models.Message{}, badRequest("input_message_content")
		}
		return msg, nil
	}

	if media, ok := inlineKinds[result.Type]; ok {
		file, err := k.fileNamed(fields, result.Type, media)
		if err != nil {
			return models.Message{}, err
		}
		fileKinds[media.kind](&msg, file)
		msg.Caption = result.Caption
		return msg, nil
	}

	switch result.Type {
	case "article":
		return models.Message{}, badRequest("input_message_content")
	case "contact":
		msg.Contact = &models.Contact{PhoneNumber: result.PhoneNumber, FirstName: result.FirstName, LastName: result.LastName}
	case "venue":
		where := models.Location{Latitude: result.Latitude, Longitude: result.Longitude}
		msg.Location, msg.Venue = &where, &models.Venue{Location: where, Title: result.Title, Address: result.Address}
	case "location":
		msg.Location = &models.Location{Latitude: result.Latitude, Longitude: result.Longitude}
	default:
		msg.Text = result.Title
	}
	return msg, nil
}

func (k *Kitchen) fileNamed(fields map[string]any, resultType string, media inlineMedia) (File, error) {
	if id, ok := fields[media.cached].(string); ok && id != "" {
		return k.files.named(id, media.kind)
	}
	if url, ok := fields[media.fetched].(string); ok && url != "" {
		return k.files.issue(media.kind, url, nil), nil
	}
	return File{}, badRequest(resultType)
}

// Search types the bot's name and a query into the compose box, which reaches
// the bot as an inline query rather than as a message.
func (m *Member) Search(query string) {
	k := m.kitchen()
	id := k.world.nextQuery()
	k.inline.ask(id, m.chat.id, m.user.id, query)

	who := m.user.identity()
	m.awaitFromNow()
	k.deliver(models.Update{InlineQuery: &models.InlineQuery{
		ID: id, From: &who, Query: query, ChatType: string(m.chat.kind),
	}})
}

func (m *Member) Pick(titleOrID string) {
	k := m.kitchen()
	if m.shutOut() {
		return
	}

	asked, answered := k.inline.newest(m.chat.id, m.user.id)
	if !answered {
		k.tb.Errorf("kitchen: %s has no answered search to pick from", m)
		return
	}

	var chosen offer
	var offered []string
	for _, one := range asked.offers {
		offered = append(offered, one.title)
		if one.id == titleOrID || one.title == titleOrID {
			chosen = one
		}
	}
	if chosen.id == "" {
		k.tb.Errorf("kitchen: the bot offered %s no %q, only: %s", m, titleOrID, strings.Join(offered, ", "))
		return
	}

	who := m.user.identity()
	sent := chosen.message
	sent.From = &who
	bot := k.botUser()
	sent.ViaBot = &bot

	m.awaitFromNow()
	landed := k.world.add(m.chat.id, sent)
	m.awaiting = landed.ID
	k.deliver(models.Update{ChosenInlineResult: &models.ChosenInlineResult{
		ResultID: chosen.id, From: who, Query: asked.query,
	}})
}
