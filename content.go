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

func contactName(c *models.Contact) string {
	if c.LastName == "" {
		return c.FirstName
	}
	return c.FirstName + " " + c.LastName
}
