package kitchen

import (
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
)

func findButton(markup *models.InlineKeyboardMarkup, labelOrData string) (models.InlineKeyboardButton, bool) {
	if markup == nil {
		return models.InlineKeyboardButton{}, false
	}
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if button.Text == labelOrData || button.CallbackData == labelOrData {
				return button, true
			}
		}
	}
	return models.InlineKeyboardButton{}, false
}

func buttonLabels(markup *models.InlineKeyboardMarkup) string {
	if markup == nil {
		return "none"
	}
	var labels []string
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			labels = append(labels, strconv.Quote(button.Text))
		}
	}
	if len(labels) == 0 {
		return "none"
	}
	return strings.Join(labels, ", ")
}
