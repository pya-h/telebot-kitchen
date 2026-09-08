package kitchen

import (
	"slices"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
)

// What a die may land on, by the emoji Telegram rolls it for.
var diceFaces = map[string]int{"🎲": 6, "🎯": 6, "🎳": 6, "🏀": 5, "⚽": 5, "🎰": 64}

const plainDie = "🎲"

func rollableEmoji() string {
	faces := make([]string, 0, len(diceFaces))
	for emoji := range diceFaces {
		faces = append(faces, emoji)
	}
	slices.Sort(faces)
	return strings.Join(faces, " ")
}

func (p params) place() (models.Location, error) {
	latitude, err := strconv.ParseFloat(p["latitude"], 64)
	if err != nil {
		return models.Location{}, badRequest("latitude")
	}
	longitude, err := strconv.ParseFloat(p["longitude"], 64)
	if err != nil {
		return models.Location{}, badRequest("longitude")
	}
	return models.Location{Latitude: latitude, Longitude: longitude}, nil
}

func (k *Kitchen) sendVenue(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	where, err := p.place()
	if err != nil {
		return nil, err
	}
	title, address := p["title"], p["address"]
	if title == "" {
		return nil, badRequest("title")
	}
	if address == "" {
		return nil, badRequest("address")
	}
	markup, err := k.accept(p, chatID)
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	// A venue carries its coordinates as well, the way Telegram sends both.
	return k.world.add(chatID, models.Message{
		From:        &sender,
		Location:    &where,
		Venue:       &models.Venue{Location: where, Title: title, Address: address},
		ReplyMarkup: markup,
	}), nil
}

func (k *Kitchen) sendContact(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	phone, first := p["phone_number"], p["first_name"]
	if phone == "" {
		return nil, badRequest("phone_number")
	}
	if first == "" {
		return nil, badRequest("first_name")
	}
	markup, err := k.accept(p, chatID)
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	return k.world.add(chatID, models.Message{
		From: &sender,
		Contact: &models.Contact{
			PhoneNumber: phone, FirstName: first, LastName: p["last_name"], VCard: p["vcard"],
		},
		ReplyMarkup: markup,
	}), nil
}

func (k *Kitchen) sendDice(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	emoji := p["emoji"]
	if emoji == "" {
		emoji = plainDie
	}
	faces, rollable := diceFaces[emoji]
	if !rollable {
		return nil, badRequest("emoji")
	}
	markup, err := k.accept(p, chatID)
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	return k.world.add(chatID, models.Message{
		From:        &sender,
		Dice:        &models.Dice{Emoji: emoji, Value: k.world.nextRoll(faces)},
		ReplyMarkup: markup,
	}), nil
}

func (k *Kitchen) sendPoll(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	question := p["question"]
	if question == "" {
		return nil, requestError("poll question is empty")
	}

	var asked []models.InputPollOption
	if err := p.decode("options", &asked); err != nil {
		return nil, badRequest("options")
	}
	if len(asked) < 2 {
		return nil, requestError("poll must have at least 2 option(s)")
	}
	options := make([]models.PollOption, len(asked))
	for i, option := range asked {
		if option.Text == "" {
			return nil, badRequest("options")
		}
		options[i] = models.PollOption{Text: option.Text}
	}

	var correct []int
	if err := p.decode("correct_option_ids", &correct); err != nil {
		return nil, badRequest("correct_option_ids")
	}
	for _, id := range correct {
		if id < 0 || id >= len(options) {
			return nil, requestError("option index out of range")
		}
	}
	kind := p["type"]
	if kind == "" {
		kind = "regular"
	}
	if kind == "quiz" && len(correct) == 0 {
		return nil, requestError("quiz must have an answer")
	}

	markup, err := k.accept(p, chatID)
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	return k.world.add(chatID, models.Message{
		From: &sender,
		Poll: &models.Poll{
			ID:                    k.world.nextPoll(),
			Question:              question,
			Options:               options,
			Type:                  kind,
			IsAnonymous:           anonymousBy(p),
			AllowsMultipleAnswers: p.flag("allows_multiple_answers"),
			CorrectOptionIDs:      correct,
		},
		ReplyMarkup: markup,
	}), nil
}

// A poll Telegram was told nothing about is anonymous, so an absent flag is not
// the same as a false one.
func anonymousBy(p params) bool {
	raw, given := p["is_anonymous"]
	return !given || raw == "" || raw == "true"
}

func pollOptions(poll *models.Poll) []string {
	if poll == nil {
		return nil
	}
	asked := make([]string, len(poll.Options))
	for i, option := range poll.Options {
		asked[i] = option.Text
	}
	return asked
}

func contactName(c *models.Contact) string {
	if c.LastName == "" {
		return c.FirstName
	}
	return c.FirstName + " " + c.LastName
}
