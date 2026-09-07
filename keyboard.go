package kitchen

import (
	"slices"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
)

// Menu is the reply keyboard up in this chat: the hard keys under the compose
// box, which stay put until the bot replaces or removes them.
func (c *Chat) Menu() [][]string { return c.kitchen.world.menu(c.id) }

func (m *Member) Menu() [][]string { return m.chat.Menu() }

func (m *Member) HasKey(label string) bool {
	for _, row := range m.Menu() {
		if slices.Contains(row, label) {
			return true
		}
	}
	return false
}

func buttonsOf(markup *models.InlineKeyboardMarkup) [][]Button {
	if markup == nil {
		return nil
	}
	rows := make([][]Button, len(markup.InlineKeyboard))
	for i, row := range markup.InlineKeyboard {
		rows[i] = make([]Button, len(row))
		for j, button := range row {
			rows[i][j] = Button{Label: button.Text, Data: button.CallbackData, URL: button.URL}
		}
	}
	return rows
}

// Labels break when the wording changes; callback data is translation-proof.
func findButton(rows [][]Button, labelOrData string) (Button, bool) {
	for _, row := range rows {
		for _, button := range row {
			if button.Label == labelOrData || button.Data == labelOrData {
				return button, true
			}
		}
	}
	return Button{}, false
}

func keyLabels(rows [][]string) string {
	var labels []string
	for _, row := range rows {
		for _, key := range row {
			labels = append(labels, strconv.Quote(key))
		}
	}
	if len(labels) == 0 {
		return "none"
	}
	return strings.Join(labels, ", ")
}

func buttonLabels(rows [][]Button) string {
	var labels []string
	for _, row := range rows {
		for _, button := range row {
			labels = append(labels, strconv.Quote(button.Label))
		}
	}
	if len(labels) == 0 {
		return "none"
	}
	return strings.Join(labels, ", ")
}
