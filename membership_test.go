package kitchen

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot/models"
)

// updates collects what reached the bot.
type updates struct {
	mu   sync.Mutex
	seen []models.Update
}

func (u *updates) collect(k *Kitchen) {
	k.DeliverTo(func(_ context.Context, got *models.Update) {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.seen = append(u.seen, *got)
	})
}

func (u *updates) all() []models.Update {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]models.Update(nil), u.seen...)
}

func TestABotIsAnAdministratorUntilToldOtherwise(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "Standup")

	member := chatMemberOf(t, k, team.ID(), k.botUser().ID)
	if member.Type != models.ChatMemberTypeAdministrator || !member.Administrator.CanDeleteMessages {
		t.Fatalf("bot is %+v, want an administrator with every right", member)
	}

	k.User(7).In(team).DemoteBot()

	if member := chatMemberOf(t, k, team.ID(), k.botUser().ID); member.Type != models.ChatMemberTypeMember {
		t.Errorf("bot is %+v, want an ordinary member", member)
	}
}

func TestAChannelWillNotCarryAPostTheBotMayNotMake(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	news := k.Channel(-1002, "Releases")
	k.User(7).In(news).PromoteBot(PinMessages)

	reply := callForm(t, k, "sendMessage", map[string]string{"chat_id": "-1002", "text": "v1"})
	if reply.OK || !strings.Contains(reply.Description, "administrator rights in the channel") {
		t.Errorf("reply = %+v, want the rights refusal", reply)
	}
	if log := news.History(); len(log) != 0 {
		t.Errorf("history = %v, want the refused post kept out of the channel", log)
	}
}

func TestABotOutOfAChatIsToldSoRatherThanAnswered(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	team := k.Group(-42, "Standup")
	k.User(7).In(team).RemoveBot()

	reply := callForm(t, k, "sendMessage", map[string]string{"chat_id": "-42", "text": "hello"})
	if reply.status != 403 || !strings.Contains(reply.Description, "kicked from the group chat") {
		t.Errorf("reply = %+v, want Telegram's forbidden", reply)
	}
}

func TestDeletingSomebodyElsesMessageNeedsTheRight(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	team := k.Group(-42, "Standup")
	ali := k.User(7).In(team)
	ali.Send("mine")
	k.User(9).In(team).PromoteBot(PinMessages)

	theirs := fmt.Sprint(team.History()[0].ID)
	reply := callForm(t, k, "deleteMessage", map[string]string{"chat_id": "-42", "message_id": theirs})
	if reply.OK || !strings.Contains(reply.Description, "can't be deleted") {
		t.Fatalf("reply = %+v, want the refusal a bot without the right gets", reply)
	}

	k.User(9).In(team).PromoteBot(DeleteMessages)
	if reply := callForm(t, k, "deleteMessage", map[string]string{"chat_id": "-42", "message_id": theirs}); !reply.OK {
		t.Errorf("reply = %+v, want the delete allowed once the right is there", reply)
	}
}

// Its own message is always the bot's to take back, right or no right.
func TestABotAlwaysDeletesItsOwn(t *testing.T) {
	k := New(t)
	k.DeliverTo(syncBot(t, k, echoHandler).ProcessUpdate)

	team := k.Group(-42, "Standup")
	ali := k.User(7).In(team)
	ali.Send("hello")
	reply := ali.Expect(TextIs("echo: hello"))
	k.User(9).In(team).PromoteBot(PinMessages)

	got := callForm(t, k, "deleteMessage", map[string]string{
		"chat_id": "-42", "message_id": fmt.Sprint(reply.ID),
	})
	if !got.OK {
		t.Errorf("reply = %+v, want its own message deleted", got)
	}
}

func TestAChatDescribesItselfToTheBot(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Supergroup(-1001, "Standup")
	k.User(7).In(team).Send("here")
	k.User(9).In(team).Send("here")

	var info models.ChatFullInfo
	callForm(t, k, "getChat", map[string]string{"chat_id": "-1001"}).decode(t, &info)
	if info.Type != models.ChatTypeSupergroup || info.Title != "Standup" {
		t.Errorf("chat = %+v, want the supergroup", info)
	}

	var count int
	callForm(t, k, "getChatMemberCount", map[string]string{"chat_id": "-1001"}).decode(t, &count)
	if count != 3 {
		t.Errorf("count = %d, want both members and the bot", count)
	}

	k.User(9).In(team).Promote(k.User(7), PinMessages)
	var admins []models.ChatMember
	callForm(t, k, "getChatAdministrators", map[string]string{"chat_id": "-1001"}).decode(t, &admins)
	if len(admins) != 2 || admins[0].Administrator.User.ID != k.botUser().ID || admins[1].Administrator.User.ID != 7 {
		t.Errorf("administrators = %+v, want the bot and the promoted member", admins)
	}
}

func TestJoiningIsNewsAndBeingThereIsNot(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	team := k.Group(-42, "Standup")
	ali := k.User(7, WithFullName("Ali", "Rezaei")).In(team)
	if seen := got.all(); len(seen) != 0 {
		t.Fatalf("updates = %+v, want nothing from placing a member", seen)
	}

	ali.Join()

	seen := got.all()
	if len(seen) != 2 {
		t.Fatalf("updates = %+v, want the service message and the membership change", seen)
	}
	if len(seen[0].Message.NewChatMembers) != 1 || seen[0].Message.NewChatMembers[0].ID != 7 {
		t.Errorf("service message = %+v, want Ali joining", seen[0].Message)
	}
	changed := seen[1].ChatMember
	if changed == nil || changed.OldChatMember.Type != models.ChatMemberTypeLeft ||
		changed.NewChatMember.Type != models.ChatMemberTypeMember || changed.From.ID != 7 {
		t.Errorf("chat_member = %+v, want Ali going from outside to in", changed)
	}
	if got := team.Transcript(); got != "**Ali Rezaei:** (joined)\n" {
		t.Errorf("transcript = %q, want the join as a chat records it", got)
	}
}

func TestLeavingTakesAMemberOffTheRoster(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	team := k.Group(-42, "Standup")
	ali := k.User(7).In(team)
	ali.Leave()

	seen := got.all()
	if len(seen) != 2 || seen[0].Message.LeftChatMember == nil || seen[1].ChatMember.NewChatMember.Type != models.ChatMemberTypeLeft {
		t.Fatalf("updates = %+v, want the service message and the membership change", seen)
	}
	if members := team.Members(); len(members) != 0 {
		t.Errorf("members = %v, want nobody left", members)
	}
}

// my_chat_member, not news about somebody else, is what a bot branches on.
func TestTheBotHearsItsOwnStandingChange(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	team := k.Group(-42, "Standup")
	k.User(7).In(team).RemoveBot()

	seen := got.all()
	if len(seen) != 1 || seen[0].MyChatMember == nil || seen[0].ChatMember != nil {
		t.Fatalf("updates = %+v, want one my_chat_member", seen)
	}
	changed := seen[0].MyChatMember
	if changed.OldChatMember.Type != models.ChatMemberTypeAdministrator ||
		changed.NewChatMember.Type != models.ChatMemberTypeBanned || changed.From.ID != 7 {
		t.Errorf("my_chat_member = %+v, want the bot kicked by Ali", changed)
	}
}

func chatMemberOf(t *testing.T, k *Kitchen, chatID, userID int64) models.ChatMember {
	t.Helper()
	var member models.ChatMember
	callForm(t, k, "getChatMember", map[string]string{
		"chat_id": fmt.Sprint(chatID), "user_id": fmt.Sprint(userID),
	}).decode(t, &member)
	return member
}

func TestAPrivateChatHasNoMembershipToChange(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	New(tb).User(7).Join()

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "nobody to admit or remove") {
		t.Errorf("errors = %v, want one about a private chat", errs)
	}
}
