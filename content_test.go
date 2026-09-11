package kitchen

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestABotSendsAPlaceAsBothVenueAndCoordinates(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7, Started())

	if _, err := b.SendVenue(context.Background(), &bot.SendVenueParams{
		ChatID: ada.ChatID(), Latitude: 35.7, Longitude: 51.4,
		Title: "Azadi Tower", Address: "Azadi Square",
	}); err != nil {
		t.Fatalf("venue: %v", err)
	}

	got := ada.History()[0]
	if got.Media != "venue" || got.Text != "Azadi Tower" {
		t.Errorf("message = %s, want the venue shown by its title", got)
	}
	// A client shows the venue, but the bot may still read where it is.
	raw := k.world.history(ada.ChatID())[0]
	if raw.Venue == nil || raw.Location == nil || raw.Location.Latitude != 35.7 {
		t.Errorf("raw = %+v, want the coordinates carried alongside", raw)
	}
}

func TestAVenueNeedsSomewhereToBe(t *testing.T) {
	k := New(t)
	ada := k.User(7)

	for _, missing := range []map[string]string{
		{"chat_id": "7", "latitude": "35.7", "longitude": "51.4", "address": "Azadi Square"},
		{"chat_id": "7", "latitude": "35.7", "longitude": "51.4", "title": "Azadi Tower"},
		{"chat_id": "7", "longitude": "51.4", "title": "Azadi Tower", "address": "Azadi Square"},
	} {
		if reply := callForm(t, k, "sendVenue", missing); reply.OK {
			t.Errorf("sendVenue%v was accepted, want it refused", missing)
		}
	}
	if log := ada.History(); len(log) != 0 {
		t.Errorf("history = %v, want nothing sent", log)
	}
}

func TestABotSendsAContactByName(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7, Started())

	if _, err := b.SendContact(context.Background(), &bot.SendContactParams{
		ChatID: ada.ChatID(), PhoneNumber: "+989120000000", FirstName: "Bob", LastName: "Ross",
	}); err != nil {
		t.Fatalf("contact: %v", err)
	}
	if got := ada.History()[0]; got.Media != "contact" || got.Text != "Bob Ross" {
		t.Errorf("message = %s, want the contact shown by name", got)
	}
}

func TestAContactNeedsANumberAndAName(t *testing.T) {
	k := New(t)
	for _, missing := range []map[string]string{
		{"chat_id": "7", "first_name": "Bob"},
		{"chat_id": "7", "phone_number": "+989120000000"},
	} {
		if reply := callForm(t, k, "sendContact", missing); reply.OK {
			t.Errorf("sendContact%v was accepted, want it refused", missing)
		}
	}
}

func TestARollIsRepeatable(t *testing.T) {
	rolls := func() []string {
		k := New(t)
		b := newClient(t, k)
		k.DeliverTo(func(context.Context, *models.Update) {})
		ada := k.User(7, Started())
		for range 3 {
			if _, err := b.SendDice(context.Background(), &bot.SendDiceParams{ChatID: ada.ChatID()}); err != nil {
				t.Fatalf("dice: %v", err)
			}
		}
		var faces []string
		for _, m := range ada.History() {
			faces = append(faces, m.Text)
		}
		return faces
	}

	first, again := rolls(), rolls()
	if len(first) != 3 || first[0] != "🎲 1" || first[1] != "🎲 2" {
		t.Errorf("rolls = %v, want the faces coming up in turn", first)
	}
	if strings.Join(first, ",") != strings.Join(again, ",") {
		t.Errorf("rolls = %v then %v, want the same kitchen to roll the same", first, again)
	}
}

func TestTelegramRollsOnlyTheDiceItHas(t *testing.T) {
	k := New(t)
	k.User(7, Started())
	if reply := callForm(t, k, "sendDice", map[string]string{"chat_id": "7", "emoji": "🍒"}); reply.OK {
		t.Error("a cherry was rolled, want it refused")
	}
	if reply := callForm(t, k, "sendDice", map[string]string{"chat_id": "7", "emoji": "🎰"}); !reply.OK {
		t.Errorf("reply = %+v, want a slot machine rolled", reply)
	}
}

func TestABotAsksAPollAndTheChatSeesWhatItAsks(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7, Started())

	if _, err := b.SendPoll(context.Background(), &bot.SendPollParams{
		ChatID: ada.ChatID(), Question: "Pizza or pasta?",
		Options: []models.InputPollOption{{Text: "Pizza"}, {Text: "Pasta"}},
	}); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got := ada.History()[0]
	if got.Media != "poll" || got.Text != "Pizza or pasta?" {
		t.Errorf("message = %s, want the poll shown by its question", got)
	}
	if strings.Join(got.Options, ",") != "Pizza,Pasta" {
		t.Errorf("options = %v, want what the poll asks", got.Options)
	}
	if !strings.Contains(got.String(), "- Pizza\n- Pasta") {
		t.Errorf("rendered = %q, want the options read as lines", got.String())
	}
	// Telegram makes a poll anonymous unless it is told otherwise.
	if poll := k.world.history(ada.ChatID())[0].Poll; !poll.IsAnonymous || poll.Type != "regular" {
		t.Errorf("poll = %+v, want an anonymous regular poll", poll)
	}
}

func TestAPollTelegramWouldRefuse(t *testing.T) {
	k := New(t)
	cases := map[string]map[string]string{
		"one option":     {"chat_id": "7", "question": "One?", "options": `[{"text":"Only"}]`},
		"no question":    {"chat_id": "7", "options": `[{"text":"A"},{"text":"B"}]`},
		"blank option":   {"chat_id": "7", "question": "Q?", "options": `[{"text":"A"},{"text":""}]`},
		"quiz no answer": {"chat_id": "7", "question": "Q?", "options": `[{"text":"A"},{"text":"B"}]`, "type": "quiz"},
		"answer off end": {"chat_id": "7", "question": "Q?", "options": `[{"text":"A"},{"text":"B"}]`, "type": "quiz", "correct_option_ids": "[5]"},
	}
	for name, form := range cases {
		if reply := callForm(t, k, "sendPoll", form); reply.OK {
			t.Errorf("%s was accepted, want it refused", name)
		}
	}
}

func TestAQuizKeepsItsAnswer(t *testing.T) {
	k := New(t)
	k.User(7, Started())
	reply := callForm(t, k, "sendPoll", map[string]string{
		"chat_id": "7", "question": "2+2?", "options": `[{"text":"4"},{"text":"5"}]`,
		"type": "quiz", "correct_option_ids": "[0]", "is_anonymous": "false",
	})
	if !reply.OK {
		t.Fatalf("reply = %+v, want the quiz asked", reply)
	}
	poll := k.world.history(7)[0].Poll
	if poll.Type != "quiz" || len(poll.CorrectOptionIDs) != 1 || poll.CorrectOptionIDs[0] != 0 {
		t.Errorf("poll = %+v, want a quiz answered by the first option", poll)
	}
	if poll.IsAnonymous {
		t.Error("poll is anonymous, want the flag it was given")
	}
}

func TestAMemberSendsEveryKindBack(t *testing.T) {
	k := New(t)
	var heard []string
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		kind, _ := mediaOf(u.Message)
		heard = append(heard, kind)
	})
	ada := k.User(7)

	ada.ShareVenue(35.7, 51.4, "Azadi Tower", "Azadi Square")
	ada.ShareContact("+989120000000", "Ada", "Lovelace")
	ada.RollDice("🎲", 6)
	ada.SendPoll("Pizza or pasta?", "Pizza", "Pasta")
	k.Settle()

	if strings.Join(heard, ",") != "venue,contact,dice,poll" {
		t.Errorf("bot heard %v, want each kind as itself", heard)
	}
	log := ada.History()
	if log[0].Text != "Azadi Tower" || log[1].Text != "Ada Lovelace" || log[2].Text != "🎲 6" || log[3].Text != "Pizza or pasta?" {
		t.Errorf("history = %v, want each shown by what it is", log)
	}
	// Sharing a contact is sharing your own, so it names the sender.
	if shared := k.world.history(ada.ChatID())[1].Contact; shared.UserID != 7 {
		t.Errorf("contact = %+v, want it to name the member who shared it", shared)
	}
}

func TestATestCannotRollWhatTelegramWillNot(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)

	ada.RollDice("🍒", 3)
	ada.RollDice("⚽", 6)
	ada.SendPoll("One?", "Only")

	if errs := tb.errors(); len(errs) != 3 {
		t.Fatalf("errors = %v, want one for each impossible thing", errs)
	}
	if log := ada.History(); len(log) != 0 {
		t.Errorf("history = %v, want nothing sent", log)
	}
}
