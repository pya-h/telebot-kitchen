package kitchen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestAskingToJoinIsNotJoining(t *testing.T) {
	k := New(t)
	var asked []*models.ChatJoinRequest
	members := 0
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.ChatJoinRequest != nil {
			asked = append(asked, u.ChatJoinRequest)
		}
		if u.ChatMember != nil {
			members++
		}
	})
	club := k.Group(-42, "Club")
	ada := k.User(7).In(club)

	ada.AskToJoin("let me in")
	k.Settle()

	if len(asked) != 1 || asked[0].From.ID != 7 || asked[0].Bio != "let me in" {
		t.Fatalf("requests = %+v, want one from ada with her bio", asked)
	}
	if asked[0].Chat.ID != club.ID() {
		t.Errorf("request = %+v, want it to name the chat", asked[0])
	}
	if members != 0 {
		t.Errorf("chat_member updates = %d, want asking to put nobody on the roster", members)
	}
	if roster := club.Members(); len(roster) != 0 {
		t.Errorf("roster = %v, want it empty until somebody is let in", roster)
	}
	if got := chatMemberOf(t, k, club.ID(), 7); got.Type != models.ChatMemberTypeLeft {
		t.Errorf("standing = %+v, want somebody asking to read as left", got)
	}
}

func TestApprovingLetsThemInAndNamesTheBot(t *testing.T) {
	k := New(t, alsoHearing("chat_member"))
	b := newClient(t, k)
	var changes []*models.ChatMemberUpdated
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.ChatMember != nil {
			changes = append(changes, u.ChatMember)
		}
	})
	club := k.Group(-42, "Club")
	ada := k.User(7).In(club)
	ada.AskToJoin()
	k.Settle()

	if _, err := b.ApproveChatJoinRequest(context.Background(), &bot.ApproveChatJoinRequestParams{
		ChatID: club.ID(), UserID: 7,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	k.Settle()

	if len(changes) != 1 {
		t.Fatalf("changes = %d, want the one the approval made", len(changes))
	}
	if !changes[0].From.IsBot {
		t.Errorf("change = %+v, want the bot named as what changed them", changes[0])
	}
	if changes[0].NewChatMember.Type != models.ChatMemberTypeMember {
		t.Errorf("change = %+v, want them a member now", changes[0])
	}
	if roster := club.Members(); len(roster) != 1 {
		t.Errorf("roster = %v, want ada on it", roster)
	}
	// The request is answered, so it cannot be answered twice.
	if _, err := b.ApproveChatJoinRequest(context.Background(), &bot.ApproveChatJoinRequestParams{
		ChatID: club.ID(), UserID: 7,
	}); err == nil {
		t.Error("the request was approved twice, want the second refused")
	}
}

func TestTheBotMayWriteToSomebodyWaitingToJoin(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ctx := context.Background()
	send := func(chatID int64) error {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "we will get back to you"})
		return err
	}
	club := k.Group(-42, "Club")
	ada, grace := k.User(7), k.User(8)
	ada.In(club).AskToJoin()
	grace.In(club).AskToJoin()

	if err := send(ada.ID()); err != nil {
		t.Errorf("while ada's request waits: %v", err)
	}
	if _, err := b.DeclineChatJoinRequest(ctx, &bot.DeclineChatJoinRequestParams{ChatID: club.ID(), UserID: ada.ID()}); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if err := send(ada.ID()); err == nil {
		t.Error("once ada's request is answered: sent, want it forbidden")
	}

	k.Clock().Advance(knockWindow - time.Second)
	if err := send(grace.ID()); err != nil {
		t.Errorf("a second before the window closes: %v", err)
	}
	k.Clock().Advance(time.Second)
	if err := send(grace.ID()); err == nil || !strings.Contains(err.Error(), "can't initiate") {
		t.Errorf("five minutes after grace asked: err = %v, want it forbidden", err)
	}
}

func TestDecliningLeavesThemOutside(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	members := 0
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.ChatMember != nil {
			members++
		}
	})
	club := k.Group(-42, "Club")
	ada := k.User(7).In(club)
	ada.AskToJoin()
	k.Settle()

	if _, err := b.DeclineChatJoinRequest(context.Background(), &bot.DeclineChatJoinRequestParams{
		ChatID: club.ID(), UserID: 7,
	}); err != nil {
		t.Fatalf("decline: %v", err)
	}
	k.Settle()

	if members != 0 {
		t.Errorf("chat_member updates = %d, want a decline to change nobody's standing", members)
	}
	if roster := club.Members(); len(roster) != 0 {
		t.Errorf("roster = %v, want it still empty", roster)
	}
	// Declined is answered too, so asking again is a fresh request.
	ada.AskToJoin()
	k.Settle()
	if _, err := b.ApproveChatJoinRequest(context.Background(), &bot.ApproveChatJoinRequestParams{
		ChatID: club.ID(), UserID: 7,
	}); err != nil {
		t.Errorf("approve after a fresh request: %v", err)
	}
}

func TestAnsweringNeedsTheRightAndARequest(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	club := k.Group(-42, "Club")
	ada := k.User(7).In(club)

	// Nobody has asked.
	if _, err := b.ApproveChatJoinRequest(context.Background(), &bot.ApproveChatJoinRequestParams{
		ChatID: club.ID(), UserID: 7,
	}); err == nil {
		t.Error("a request nobody made was approved, want it refused")
	}

	ada.AskToJoin()
	k.Settle()
	ada.PromoteBot(PinMessages) // anything but the right to invite

	if _, err := b.ApproveChatJoinRequest(context.Background(), &bot.ApproveChatJoinRequestParams{
		ChatID: club.ID(), UserID: 7,
	}); err == nil || !strings.Contains(err.Error(), "not enough rights") {
		t.Errorf("err = %v, want the missing right named", err)
	}
}

func TestAskingTwiceOrFromInsideIsRefused(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	club := k.Group(-42, "Club")
	ada, bob := k.User(7).In(club), k.User(8).In(club)

	ada.AskToJoin()
	ada.AskToJoin()

	bob.Join()
	bob.AskToJoin()

	k.User(9).AskToJoin() // a private chat has nobody to admit

	if errs := tb.errors(); len(errs) != 3 {
		t.Fatalf("errors = %v, want one for each request Telegram would not take", errs)
	}
}

func TestABoostAndTakingItBack(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var added []*models.ChatBoostUpdated
	var removed []*models.ChatBoostRemoved
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.ChatBoost != nil {
			added = append(added, u.ChatBoost)
		}
		if u.RemovedChatBoost != nil {
			removed = append(removed, u.RemovedChatBoost)
		}
	})
	news := k.Channel(-1001, "News")
	ada := k.User(7).In(news)

	ada.Boost()
	k.Settle()

	if len(added) != 1 || added[0].Boost.Source.ChatBoostSourcePremium.User.ID != 7 {
		t.Fatalf("boosts = %+v, want one from ada", added)
	}
	if added[0].Boost.ExpirationDate <= added[0].Boost.AddDate {
		t.Errorf("boost = %+v, want it to lapse after it starts", added[0].Boost)
	}

	held, err := b.GetUserChatBoosts(context.Background(), &bot.GetUserChatBoostsParams{ChatID: news.ID(), UserID: 7})
	if err != nil {
		t.Fatalf("boosts: %v", err)
	}
	if len(held.Boosts) != 1 || held.Boosts[0].BoostID != added[0].Boost.BoostID {
		t.Errorf("held = %+v, want the boost ada put on it", held)
	}

	ada.Unboost()
	k.Settle()
	if len(removed) != 1 || removed[0].BoostID != added[0].Boost.BoostID {
		t.Fatalf("removals = %+v, want the boost it took back named", removed)
	}
	if held, _ := b.GetUserChatBoosts(context.Background(), &bot.GetUserChatBoostsParams{ChatID: news.ID(), UserID: 7}); len(held.Boosts) != 0 {
		t.Errorf("held = %+v, want nothing left", held)
	}
}

func TestBoostingTwiceOrNotAtAll(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	news := k.Channel(-1001, "News")
	ada := k.User(7).In(news)

	ada.Boost()
	ada.Boost()
	ada.Unboost()
	ada.Unboost()

	if errs := tb.errors(); len(errs) != 2 {
		t.Fatalf("errors = %v, want one for the second boost and one for the second removal", errs)
	}
}

// Both of these carry a union the library marshals through a pointer method, so
// the wire is the only place to find out whether it reaches the bot whole.
func TestJoinRequestsAndBoostsSurviveTheWebhookSeam(t *testing.T) {
	k := New(t)
	bodies := make(chan []byte, 4)
	k.DeliverToWebhook(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- body
	}))
	news := k.Channel(-1001, "News")
	ada := k.User(7).In(news)

	ada.AskToJoin("hello")
	asked := <-bodies
	ada.Boost()
	boosted := <-bodies

	var request, boost models.Update
	if err := json.Unmarshal(asked, &request); err != nil {
		t.Fatalf("the join request did not survive the wire: %v\n%s", err, asked)
	}
	if request.ChatJoinRequest == nil || request.ChatJoinRequest.Bio != "hello" {
		t.Errorf("update = %s, want the request readable the other side", asked)
	}
	if err := json.Unmarshal(boosted, &boost); err != nil {
		t.Fatalf("the boost did not survive the wire: %v\n%s", err, boosted)
	}
	if boost.ChatBoost == nil || boost.ChatBoost.Boost.Source.ChatBoostSourcePremium.User.ID != 7 {
		t.Errorf("update = %s, want the boost source readable the other side", boosted)
	}
}
