package kitchen

import "github.com/go-telegram/bot/models"

// The calls a bot makes before it acts, answered from the roster.
func (k *Kitchen) getChat(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	info, found := k.world.info(chatID)
	if !found {
		return nil, requestError("chat not found")
	}
	return models.ChatFullInfo{
		ID:        info.ID,
		Type:      info.Type,
		Title:     info.Title,
		Username:  info.Username,
		FirstName: info.FirstName,
		LastName:  info.LastName,
	}, nil
}

func (k *Kitchen) getChatMember(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	userID, err := p.chat("user_id")
	if err != nil {
		return nil, err
	}

	member, found := k.world.standingOf(chatID, userID)
	if !found {
		return nil, requestError("user not found")
	}
	// By pointer: only then does the library encode the status.
	return &member, nil
}

func (k *Kitchen) getChatAdministrators(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	if _, found := k.world.info(chatID); !found {
		return nil, requestError("chat not found")
	}
	return k.world.administrators(chatID), nil
}

func (k *Kitchen) getChatMemberCount(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	if _, found := k.world.info(chatID); !found {
		return nil, requestError("chat not found")
	}
	// The bot counts too.
	return len(k.world.roster(chatID)) + 1, nil
}
