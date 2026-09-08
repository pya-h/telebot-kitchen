package kitchen

import (
	"strconv"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
)

// How long a boost stands before it lapses. Telegram ties this to the premium
// subscription behind it; the kitchen only has to be consistent about it.
const boostPeriod = 90 * 24 * time.Hour

// boostBook keeps who is boosting what. A boost is not a membership: somebody
// may boost a channel they never joined.
type boostBook struct {
	mu sync.Mutex
	by map[int64]map[int64]models.ChatBoost
	id int
}

func newBoostBook() *boostBook { return &boostBook{by: map[int64]map[int64]models.ChatBoost{}} }

func (b *boostBook) add(chatID int64, who models.User, now time.Time) (models.ChatBoost, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	boosting, ok := b.by[chatID]
	if !ok {
		boosting = map[int64]models.ChatBoost{}
		b.by[chatID] = boosting
	}
	if _, already := boosting[who.ID]; already {
		return models.ChatBoost{}, false
	}

	b.id++
	boost := models.ChatBoost{
		BoostID:        "boost-" + strconv.Itoa(b.id),
		AddDate:        int(now.Unix()),
		ExpirationDate: int(now.Add(boostPeriod).Unix()),
		Source: models.ChatBoostSource{
			Source:                 models.ChatBoostSourceTypePremium,
			ChatBoostSourcePremium: &models.ChatBoostSourcePremium{Source: models.ChatBoostSourceTypePremium, User: who},
		},
	}
	boosting[who.ID] = boost
	return boost, true
}

func (b *boostBook) remove(chatID, userID int64) (models.ChatBoost, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	boost, boosting := b.by[chatID][userID]
	if boosting {
		delete(b.by[chatID], userID)
	}
	return boost, boosting
}

func (b *boostBook) of(chatID, userID int64) []models.ChatBoost {
	b.mu.Lock()
	defer b.mu.Unlock()

	boost, boosting := b.by[chatID][userID]
	if !boosting {
		return []models.ChatBoost{}
	}
	return []models.ChatBoost{boost}
}

func (k *Kitchen) getUserChatBoosts(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	userID, err := p.chat("user_id")
	if err != nil {
		return nil, err
	}
	if _, found := k.world.info(chatID); !found {
		return nil, requestError("chat not found")
	}
	return models.UserChatBoosts{Boosts: k.boosts.of(chatID, userID)}, nil
}

// Boost puts the member's premium boost behind the chat, which a bot hears
// about whether or not they are on the roster.
func (m *Member) Boost() {
	k := m.kitchen()
	if !m.roster() {
		return
	}

	who := m.user.identity()
	boost, added := k.boosts.add(m.chat.id, who, k.clock.Now())
	if !added {
		k.tb.Errorf("kitchen: %s is already boosting the chat", m)
		return
	}

	info, _ := k.world.info(m.chat.id)
	m.awaitFromNow()
	k.deliver(models.Update{ChatBoost: &models.ChatBoostUpdated{Chat: info, Boost: boost}})
}

// Unboost takes it back, which reaches the bot as its own update rather than as
// a boost of nothing.
func (m *Member) Unboost() {
	k := m.kitchen()
	if !m.roster() {
		return
	}

	boost, boosting := k.boosts.remove(m.chat.id, m.user.id)
	if !boosting {
		k.tb.Errorf("kitchen: %s is not boosting the chat, so there is nothing to take back", m)
		return
	}

	info, _ := k.world.info(m.chat.id)
	m.awaitFromNow()
	k.deliver(models.Update{RemovedChatBoost: &models.ChatBoostRemoved{
		Chat: info, BoostID: boost.BoostID, RemoveDate: int(k.clock.Now().Unix()), Source: boost.Source,
	}})
}
