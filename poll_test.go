package kitchen

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// pollIn is the poll a chat is showing, past whatever service messages precede it.
func pollIn(t *testing.T, k *Kitchen, chatID int64) models.Poll {
	t.Helper()
	poll, asked := k.world.newestPoll(chatID)
	if !asked {
		t.Fatalf("chat %d is showing no poll", chatID)
	}
	return poll
}

// messageWithPoll is the id of the message carrying the chat's poll.
func messageWithPoll(t *testing.T, k *Kitchen, chatID int64) int {
	t.Helper()
	for _, m := range k.world.history(chatID) {
		if m.Poll != nil {
			return m.ID
		}
	}
	t.Fatalf("chat %d carries no poll", chatID)
	return 0
}

// asked sends a poll as the bot and hands back the chat it landed in.
func asked(t *testing.T, k *Kitchen, b *bot.Bot, ada *User, p *bot.SendPollParams) {
	t.Helper()
	p.ChatID = ada.ChatID()
	if _, err := b.SendPoll(context.Background(), p); err != nil {
		t.Fatalf("poll: %v", err)
	}
}

func TestAVoteReachesTheBotTwiceOverAndCounts(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	var answers []*models.PollAnswer
	var states []*models.Poll
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		switch {
		case u.PollAnswer != nil:
			answers = append(answers, u.PollAnswer)
		case u.Poll != nil:
			states = append(states, u.Poll)
		}
	})
	ada := k.User(7)

	asked(t, k, b, ada, &bot.SendPollParams{
		Question:    "Pizza or pasta?",
		Options:     []models.InputPollOption{{Text: "Pizza"}, {Text: "Pasta"}},
		IsAnonymous: bot.False(),
	})
	ada.Vote("Pasta")
	k.Settle()

	if len(answers) != 1 || answers[0].User.ID != 7 || len(answers[0].OptionIDs) != 1 || answers[0].OptionIDs[0] != 1 {
		t.Fatalf("answers = %+v, want one naming ada and the second option", answers)
	}
	if len(states) != 1 || states[0].TotalVoterCount != 1 || states[0].Options[1].VoterCount != 1 {
		t.Fatalf("states = %+v, want the tally to follow the vote", states)
	}
	if states[0].Options[0].VoterCount != 0 {
		t.Errorf("state = %+v, want nothing counted against the option nobody chose", states[0])
	}
}

func TestAnAnonymousPollTellsTheBotTheTallyAndNotTheVoter(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	var answers, states int
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		switch {
		case u.PollAnswer != nil:
			answers++
		case u.Poll != nil:
			states++
		}
	})
	team := k.Group(-42, "Standup")
	ada, bob := k.User(7).In(team), k.User(8).In(team)
	ada.Join()
	bob.Join()

	if _, err := b.SendPoll(context.Background(), &bot.SendPollParams{
		ChatID:   team.ID(),
		Question: "Pizza or pasta?",
		Options:  []models.InputPollOption{{Text: "Pizza"}, {Text: "Pasta"}},
	}); err != nil {
		t.Fatalf("poll: %v", err)
	}
	ada.Vote("Pizza")
	bob.Vote("Pizza")
	k.Settle()

	if answers != 0 {
		t.Errorf("answers = %d, want an anonymous poll to name nobody", answers)
	}
	if states != 2 {
		t.Errorf("states = %d, want the tally after each vote", states)
	}
	if got := pollIn(t, k, team.ID()); got.Options[0].VoterCount != 2 || got.TotalVoterCount != 2 {
		t.Errorf("poll = %+v, want both counted", got)
	}
}

// A poll update is the state at that moment. If it shared the message's options
// the next vote would rewrite it, and a bot holding the earlier one would see a
// tally it was never sent.
func TestAPollUpdateKeepsTheTallyItWasSentWith(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var states []*models.Poll
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.Poll != nil {
			states = append(states, u.Poll)
		}
	})
	team := k.Group(-42, "Standup")
	ada, bob := k.User(7).In(team), k.User(8).In(team)
	ada.Join()
	bob.Join()

	if _, err := b.SendPoll(context.Background(), &bot.SendPollParams{
		ChatID: team.ID(), Question: "Pizza or pasta?",
		Options: []models.InputPollOption{{Text: "Pizza"}, {Text: "Pasta"}},
	}); err != nil {
		t.Fatalf("poll: %v", err)
	}
	ada.Vote("Pizza")
	k.Settle()
	bob.Vote("Pizza")
	k.Settle()

	if len(states) != 2 {
		t.Fatalf("states = %d, want one per vote", len(states))
	}
	if states[0].Options[0].VoterCount != 1 {
		t.Errorf("the first update now reads %d votes, want the 1 it was sent with", states[0].Options[0].VoterCount)
	}
	if states[1].Options[0].VoterCount != 2 {
		t.Errorf("the second update reads %d votes, want 2", states[1].Options[0].VoterCount)
	}
}

func TestChangingAnAnswerReplacesItRatherThanAddingOne(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var last *models.Poll
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.Poll != nil {
			last = u.Poll
		}
	})
	ada := k.User(7)

	asked(t, k, b, ada, &bot.SendPollParams{
		Question:    "Pizza or pasta?",
		Options:     []models.InputPollOption{{Text: "Pizza"}, {Text: "Pasta"}},
		IsAnonymous: bot.False(),
	})
	ada.Vote("Pizza")
	ada.Vote("Pasta")
	k.Settle()

	if last.TotalVoterCount != 1 || last.Options[0].VoterCount != 0 || last.Options[1].VoterCount != 1 {
		t.Errorf("poll = %+v, want one voter counted once, against the answer they changed to", last)
	}
}

func TestARetractedVoteNamesNoOption(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var answers []*models.PollAnswer
	var last *models.Poll
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.PollAnswer != nil {
			answers = append(answers, u.PollAnswer)
		}
		if u.Poll != nil {
			last = u.Poll
		}
	})
	ada := k.User(7)

	asked(t, k, b, ada, &bot.SendPollParams{
		Question:    "Pizza or pasta?",
		Options:     []models.InputPollOption{{Text: "Pizza"}, {Text: "Pasta"}},
		IsAnonymous: bot.False(),
	})
	ada.Vote("Pizza")
	ada.RetractVote()
	k.Settle()

	if len(answers) != 2 || len(answers[1].OptionIDs) != 0 {
		t.Fatalf("answers = %+v, want the second to name no option", answers)
	}
	if last.TotalVoterCount != 0 || last.Options[0].VoterCount != 0 {
		t.Errorf("poll = %+v, want the vote taken back off the tally", last)
	}
}

func TestSeveralAnswersOnlyWhereThePollAllowsThem(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)

	asked(t, k, b, ada, &bot.SendPollParams{
		Question:              "Toppings?",
		Options:               []models.InputPollOption{{Text: "Olive"}, {Text: "Basil"}, {Text: "Chilli"}},
		AllowsMultipleAnswers: true,
	})
	ada.Vote("Olive", "Chilli")
	k.Settle()

	got := pollIn(t, k, ada.ChatID())
	if got.Options[0].VoterCount != 1 || got.Options[2].VoterCount != 1 || got.TotalVoterCount != 1 {
		t.Errorf("poll = %+v, want both answers counted for the one voter", got)
	}
}

func TestABotStopsItsOwnPollAndIsNotToldItDid(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	states := 0
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.Poll != nil {
			states++
		}
	})
	ada := k.User(7)

	asked(t, k, b, ada, &bot.SendPollParams{
		Question: "Pizza or pasta?",
		Options:  []models.InputPollOption{{Text: "Pizza"}, {Text: "Pasta"}},
	})
	sentID := ada.History()[0].ID
	ada.Vote("Pizza")
	k.Settle()

	final, err := b.StopPoll(context.Background(), &bot.StopPollParams{ChatID: ada.ChatID(), MessageID: sentID})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !final.IsClosed || final.Options[0].VoterCount != 1 {
		t.Errorf("final = %+v, want it closed with the votes it took", final)
	}
	k.Settle()
	if states != 1 {
		t.Errorf("poll updates = %d, want only the one the vote caused", states)
	}

	if _, err := b.StopPoll(context.Background(), &bot.StopPollParams{ChatID: ada.ChatID(), MessageID: sentID}); err == nil {
		t.Error("the poll was stopped twice, want the second refused")
	}
}

func TestNobodyAnswersAClosedPoll(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)

	if _, err := b.SendPoll(context.Background(), &bot.SendPollParams{
		ChatID: ada.ChatID(), Question: "Pizza or pasta?",
		Options: []models.InputPollOption{{Text: "Pizza"}, {Text: "Pasta"}},
	}); err != nil {
		t.Fatalf("poll: %v", err)
	}
	sentID := ada.History()[0].ID
	if _, err := b.StopPoll(context.Background(), &bot.StopPollParams{ChatID: ada.ChatID(), MessageID: sentID}); err != nil {
		t.Fatalf("stop: %v", err)
	}

	ada.Vote("Pizza")
	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "closed") {
		t.Errorf("errors = %v, want one about the closed poll", errs)
	}
}

func TestAMembersOwnPollIsNotTheBotsToHearOrStop(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	updates := 0
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.Poll != nil || u.PollAnswer != nil {
			updates++
		}
	})
	team := k.Group(-42, "Standup")
	ada, bob := k.User(7).In(team), k.User(8).In(team)
	ada.Join()
	bob.Join()

	ada.SendPoll("Pizza or pasta?", "Pizza", "Pasta")
	k.Settle()
	sentID := messageWithPoll(t, k, team.ID())

	bob.Vote("Pasta")
	k.Settle()

	if updates != 0 {
		t.Errorf("updates = %d, want the bot told nothing about a poll it did not send", updates)
	}
	// The tally is still kept, so the chat reads the way a client shows it.
	if got := pollIn(t, k, team.ID()); got.Options[1].VoterCount != 1 {
		t.Errorf("poll = %+v, want the vote counted anyway", got)
	}
	if _, err := b.StopPoll(context.Background(), &bot.StopPollParams{ChatID: team.ID(), MessageID: sentID}); err == nil {
		t.Error("the bot stopped somebody else's poll, want it refused")
	}
}

func TestAnAnswerTelegramWouldNotTake(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)

	ada.Vote("Pizza") // nothing asked yet

	if _, err := b.SendPoll(context.Background(), &bot.SendPollParams{
		ChatID: ada.ChatID(), Question: "Pizza or pasta?",
		Options: []models.InputPollOption{{Text: "Pizza"}, {Text: "Pasta"}},
	}); err != nil {
		t.Fatalf("poll: %v", err)
	}
	ada.Vote("Sushi")          // not on the menu
	ada.Vote("Pizza", "Pasta") // one answer only
	ada.Vote()                 // nothing chosen

	if errs := tb.errors(); len(errs) != 4 {
		t.Fatalf("errors = %v, want one for each answer Telegram would not take", errs)
	}
	if got := pollIn(t, k, ada.ChatID()); got.TotalVoterCount != 0 {
		t.Errorf("poll = %+v, want nothing counted", got)
	}
}

func TestStoppingWhatIsNotAPoll(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)

	if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: ada.ChatID(), Text: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sentID := ada.History()[0].ID
	if _, err := b.StopPoll(context.Background(), &bot.StopPollParams{ChatID: ada.ChatID(), MessageID: sentID}); err == nil {
		t.Error("a plain message was stopped, want it refused")
	}
}

// A stored poll changes in place as the votes come in, so one that went out in
// an update has to be the bot's own copy rather than the world's.
func TestAPollTheBotWasHandedDoesNotChangeUnderIt(t *testing.T) {
	k := New(t)
	var seen *models.Poll
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.Message != nil && u.Message.Poll != nil && seen == nil {
			seen = u.Message.Poll
		}
	})
	team := k.Group(-42, "Standup")
	ada, bob := k.User(7).In(team), k.User(8).In(team)
	ada.Join()
	bob.Join()
	k.Settle()

	ada.SendPoll("lunch?", "pizza", "soup")
	k.Settle()
	if seen == nil {
		t.Fatal("the bot was never handed the poll")
	}

	bob.Vote("pizza")
	k.Settle()

	if seen.Options[0].VoterCount != 0 || seen.TotalVoterCount != 0 {
		t.Errorf("the poll the bot was handed grew a vote it was never told about: %+v", seen.Options)
	}
}
