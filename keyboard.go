package kitchen

import (
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
)

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

// A label is what the user reads, so tests written against it break when the
// wording changes; callback data is the stable, translation-proof alternative.
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
