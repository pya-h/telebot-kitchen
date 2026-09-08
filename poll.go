package kitchen

import (
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/go-telegram/bot/models"
)

// pollIndex says where a poll lives and who has answered it. Everything else a
// poll knows stays in the message carrying it, so a tally has one owner.
type pollIndex struct {
	mu    sync.Mutex
	where map[string]*pollAt
}

type pollAt struct {
	chatID    int64
	messageID int
	byBot     bool
	votes     map[int64][]int
}

func newPollIndex() *pollIndex { return &pollIndex{where: map[string]*pollAt{}} }

func (i *pollIndex) put(id string, chatID int64, messageID int, byBot bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.where[id] = &pollAt{chatID: chatID, messageID: messageID, byBot: byBot, votes: map[int64][]int{}}
}

func (i *pollIndex) at(id string) (pollAt, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()

	found, ok := i.where[id]
	if !ok {
		return pollAt{}, false
	}
	return *found, true
}

// cast records an answer, or takes one back when nothing is chosen, and hands
// out the whole tally rather than a count, since a vote can change.
func (i *pollIndex) cast(id string, voter int64, chosen []int) map[int64][]int {
	i.mu.Lock()
	defer i.mu.Unlock()

	at, ok := i.where[id]
	if !ok {
		return nil
	}
	if len(chosen) == 0 {
		delete(at.votes, voter)
	} else {
		at.votes[voter] = chosen
	}
	return maps.Clone(at.votes)
}

func tallied(p *models.Poll, votes map[int64][]int) {
	for i := range p.Options {
		p.Options[i].VoterCount = 0
	}
	for _, chosen := range votes {
		for _, i := range chosen {
			if i < len(p.Options) {
				p.Options[i].VoterCount++
			}
		}
	}
	p.TotalVoterCount = len(votes)
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
	poll := &models.Poll{
		ID:                    k.world.nextPoll(),
		Question:              question,
		Options:               options,
		Type:                  kind,
		IsAnonymous:           anonymousBy(p),
		AllowsMultipleAnswers: p.flag("allows_multiple_answers"),
		CorrectOptionIDs:      correct,
	}
	sent := k.world.add(chatID, models.Message{From: &sender, Poll: poll, ReplyMarkup: markup})
	k.polls.put(poll.ID, chatID, sent.ID, true)
	return sent, nil
}

// A poll Telegram was told nothing about is anonymous, so an absent flag is not
// the same as a false one.
func anonymousBy(p params) bool {
	raw, given := p["is_anonymous"]
	return !given || raw == "" || raw == "true"
}

// Closing a poll hands the bot its final state and makes no update: the bot is
// never told what it did itself.
func (k *Kitchen) stopPoll(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	messageID, err := p.messageID()
	if err != nil {
		return nil, err
	}

	current, carried := k.world.onPoll(chatID, messageID, func(*models.Poll) {})
	if !carried {
		return nil, requestError("message is not a poll")
	}
	// Telegram lets a bot stop only the polls it sent.
	if at, indexed := k.polls.at(current.ID); !indexed || !at.byBot {
		return nil, requestError("poll can't be stopped")
	}

	stopped := false
	final, _ := k.world.onPoll(chatID, messageID, func(poll *models.Poll) {
		if !poll.IsClosed {
			poll.IsClosed, stopped = true, true
		}
	})
	if !stopped {
		return nil, requestError("poll has already been closed")
	}
	return final, nil
}

func (m *Member) SendPoll(question string, options ...string) {
	k := m.kitchen()
	if len(options) < 2 {
		k.tb.Errorf("kitchen: a poll asks at least two options, %q got %d", question, len(options))
		return
	}
	asked := make([]models.PollOption, len(options))
	for i, option := range options {
		asked[i] = models.PollOption{Text: option}
	}

	poll := &models.Poll{
		ID:          k.world.nextPoll(),
		Question:    question,
		Options:     asked,
		Type:        "regular",
		IsAnonymous: true,
	}
	if sent, said := m.say(models.Message{Poll: poll}); said {
		k.polls.put(poll.ID, m.chat.id, sent.ID, false)
	}
}

// Vote answers the newest poll in the chat by the options' own labels, the way
// somebody tapping one would.
func (m *Member) Vote(options ...string) {
	if len(options) == 0 {
		m.kitchen().tb.Errorf("kitchen: %s chose nothing; RetractVote is how a vote is taken back", m)
		return
	}
	m.vote(options)
}

// RetractVote takes back what the member answered, which reaches the bot as a
// poll answer naming no option.
func (m *Member) RetractVote() { m.vote(nil) }

func (m *Member) vote(labels []string) {
	k := m.kitchen()

	poll, asked := k.world.newestPoll(m.chat.id)
	if !asked {
		k.tb.Errorf("kitchen: %s has no poll to answer", m)
		return
	}
	if poll.IsClosed {
		k.tb.Errorf("kitchen: the poll %q is closed, so %s cannot answer it", poll.Question, m)
		return
	}

	chosen, ok := m.chose(poll, labels)
	if !ok {
		return
	}
	at, indexed := k.polls.at(poll.ID)
	if !indexed {
		k.tb.Errorf("kitchen: the poll %q is not one the kitchen handed out", poll.Question)
		return
	}

	m.awaitFromNow()
	votes := k.polls.cast(poll.ID, m.user.id, chosen)
	now, still := k.world.onPoll(at.chatID, at.messageID, func(p *models.Poll) { tallied(p, votes) })
	// A bot hears nothing about a poll it did not send.
	if !still || !at.byBot {
		return
	}
	if !now.IsAnonymous {
		voter := m.user.identity()
		k.deliver(models.Update{PollAnswer: &models.PollAnswer{
			PollID: now.ID, User: &voter, OptionIDs: chosen,
		}})
	}
	k.deliver(models.Update{Poll: &now})
}

func (m *Member) chose(poll models.Poll, labels []string) ([]int, bool) {
	k := m.kitchen()

	chosen := make([]int, 0, len(labels))
	for _, label := range labels {
		i := slices.IndexFunc(poll.Options, func(o models.PollOption) bool { return o.Text == label })
		if i < 0 {
			k.tb.Errorf("kitchen: the poll %q does not offer %q, only: %s",
				poll.Question, label, strings.Join(pollOptions(&poll), ", "))
			return nil, false
		}
		if slices.Contains(chosen, i) {
			k.tb.Errorf("kitchen: %s chose %q twice", m, label)
			return nil, false
		}
		chosen = append(chosen, i)
	}
	if len(chosen) > 1 && !poll.AllowsMultipleAnswers {
		k.tb.Errorf("kitchen: the poll %q takes one answer, %s gave %d", poll.Question, m, len(chosen))
		return nil, false
	}
	return chosen, true
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
