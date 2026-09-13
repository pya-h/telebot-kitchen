package kitchen

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestAMemberRewordingWhatTheySaidIsAnEdit(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	team := k.Group(-42, "Standup")
	alan := k.User(7).In(team)
	alan.Send("half nine")
	alan.Edit(team.History()[0], "half ten")

	seen := got.all()
	if len(seen) != 2 || seen[1].EditedMessage == nil {
		t.Fatalf("updates = %+v, want the message and then its edit", seen)
	}
	if edited := seen[1].EditedMessage; edited.Text != "half ten" || edited.ID != seen[0].Message.ID {
		t.Errorf("edit = %+v, want the same message reworded", edited)
	}
	if edited := seen[1].EditedMessage; edited.EditDate == 0 {
		t.Error("edit carries no date, which is what marks it edited")
	}
}

func TestOnlyYourOwnMessageIsYoursToEdit(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(syncBot(t, k, echoHandler).ProcessUpdate)

	team := k.Group(-42, "Standup")
	alan := k.User(7).In(team)
	alan.Send("hello")
	reply := alan.Expect(TextIs("echo: hello"))
	alan.Edit(reply, "something else")

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "not user 7 in \"Standup\"'s to edit") {
		t.Errorf("errors = %v, want one about editing the bot's message", errs)
	}
}

// In a channel an administrator may edit what somebody else published.
func TestEditingSomebodyElsesPostNeedsTheRight(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	news := k.Channel(-1002, "Releases")
	post := news.Post("v1 is out")
	k.User(7).In(news).PromoteBot(PostMessages)

	edit := map[string]string{"chat_id": "-1002", "message_id": fmt.Sprint(post.ID), "text": "v1.0.1"}
	if reply := callForm(t, k, "editMessageText", edit); reply.OK || !strings.Contains(reply.Description, "can't be edited") {
		t.Fatalf("reply = %+v, want the refusal without the right", reply)
	}

	k.User(7).In(news).PromoteBot(PostMessages, EditMessages)
	if reply := callForm(t, k, "editMessageText", edit); !reply.OK {
		t.Errorf("reply = %+v, want the edit allowed once the right is there", reply)
	}
}

func TestPinningNeedsTheRightAndIsWrittenInTheChat(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	team := k.Group(-42, "Standup")
	alan := k.User(7).In(team)
	alan.Send("read this")
	pin := map[string]string{"chat_id": "-42", "message_id": fmt.Sprint(team.History()[0].ID)}

	alan.PromoteBot(PostMessages)
	if reply := callForm(t, k, "pinChatMessage", pin); reply.OK || !strings.Contains(reply.Description, "not enough rights to pin") {
		t.Fatalf("reply = %+v, want the refusal without the right", reply)
	}

	alan.PromoteBot(PinMessages)
	if reply := callForm(t, k, "pinChatMessage", pin); !reply.OK {
		t.Fatalf("reply = %+v, want the pin allowed", reply)
	}
	pinned, ok := team.Pinned()
	if !ok || pinned.Text != "read this" {
		t.Errorf("pinned = %+v, %v; want the message that was pinned", pinned, ok)
	}
	if got := team.History()[1].Event; got != "pinned" {
		t.Errorf("chat records %q, want the pin written in it", got)
	}

	if reply := callForm(t, k, "unpinChatMessage", map[string]string{"chat_id": "-42"}); !reply.OK {
		t.Fatalf("reply = %+v, want the unpin allowed", reply)
	}
	if _, ok := team.Pinned(); ok {
		t.Error("a pin survived being taken back")
	}
}

func TestARestrictedMemberIsNotHeard(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "Standup")
	alan := k.User(7).In(team)
	alan.Send("first")

	reply := callForm(t, k, "restrictChatMember", map[string]string{
		"chat_id": "-42", "user_id": "7", "permissions": `{"can_send_messages":false}`,
	})
	if !reply.OK {
		t.Fatalf("reply = %+v, want the restriction applied", reply)
	}

	alan.Send("second")
	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "restricted") {
		t.Fatalf("errors = %v, want one about the restriction", errs)
	}
	if log := team.History(); len(log) != 1 {
		t.Errorf("history = %v, want only what was said before the restriction", log)
	}
	if member := chatMemberOf(t, k, -42, 7); member.Type != models.ChatMemberTypeRestricted || member.Restricted.CanSendMessages {
		t.Errorf("member = %+v, want them restricted from speaking", member)
	}
}

func TestManagingMembersNeedsTheRight(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	team := k.Group(-42, "Standup")
	alan := k.User(7).In(team)
	alan.Send("here")
	alan.PromoteBot(PostMessages)

	cases := map[string]map[string]string{
		"banChatMember":      {"chat_id": "-42", "user_id": "7"},
		"promoteChatMember":  {"chat_id": "-42", "user_id": "7", "can_pin_messages": "true"},
		"restrictChatMember": {"chat_id": "-42", "user_id": "7", "permissions": `{"can_send_messages":true}`},
	}
	for method, fields := range cases {
		if reply := callForm(t, k, method, fields); reply.OK || !strings.Contains(reply.Description, "not enough rights") {
			t.Errorf("%s = %+v, want the refusal without the right", method, reply)
		}
	}

	alan.PromoteBot(RestrictMembers, PromoteMembers)
	callForm(t, k, "promoteChatMember", cases["promoteChatMember"])
	member := chatMemberOf(t, k, -42, 7)
	if member.Type != models.ChatMemberTypeAdministrator || !member.Administrator.CanPinMessages || member.Administrator.CanDeleteMessages {
		t.Errorf("member = %+v, want an administrator with only what was granted", member)
	}

	// Telegram spells a demotion as a promotion to nothing.
	callForm(t, k, "promoteChatMember", map[string]string{"chat_id": "-42", "user_id": "7"})
	if member := chatMemberOf(t, k, -42, 7); member.Type != models.ChatMemberTypeMember {
		t.Errorf("member = %+v, want an ordinary member again", member)
	}
}

// A promotion may name only rights the kitchen reports and never refuses a call
// for; granting nothing is a demotion, but granting those is not.
func TestGrantingOnlyTheRightsTheKitchenReportsIsStillAPromotion(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "Standup")
	k.User(7).In(team).Join()

	callForm(t, k, "promoteChatMember", map[string]string{
		"chat_id": "-42", "user_id": "7", "can_manage_chat": "true",
	})
	if member := chatMemberOf(t, k, -42, 7); member.Type != models.ChatMemberTypeAdministrator {
		t.Errorf("member = %+v, want them promoted", member)
	}
}

// Telegram restricts somebody who is not in the chat without putting them back.
func TestRestrictingSomebodyWhoLeftDoesNotPutThemBack(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "Standup")
	alan := k.User(7).In(team)
	alan.Join()
	alan.Leave()

	callForm(t, k, "restrictChatMember", map[string]string{
		"chat_id": "-42", "user_id": "7", "permissions": `{"can_send_messages":false}`,
	})

	member := chatMemberOf(t, k, -42, 7)
	if member.Type != models.ChatMemberTypeRestricted || member.Restricted.IsMember {
		t.Errorf("member = %+v, want them restricted and still out of the chat", member)
	}
	if roster := team.Members(); len(roster) != 0 {
		t.Errorf("roster = %v, want the restriction to have left it empty", roster)
	}
}

func TestBanningTakesAMemberOffTheRoster(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	team := k.Group(-42, "Standup")
	k.User(7).In(team).Send("here")

	if reply := callForm(t, k, "banChatMember", map[string]string{"chat_id": "-42", "user_id": "7"}); !reply.OK {
		t.Fatalf("reply = %+v, want the ban applied", reply)
	}
	if members := team.Members(); len(members) != 0 {
		t.Errorf("members = %v, want nobody left", members)
	}

	callForm(t, k, "unbanChatMember", map[string]string{"chat_id": "-42", "user_id": "7"})
	if member := chatMemberOf(t, k, -42, 7); member.Type != models.ChatMemberTypeLeft {
		t.Errorf("member = %+v, want them free to come back but not back", member)
	}
}

func TestAMigratedGroupNamesItsSupergroup(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	team := k.Group(-42, "Standup")
	k.User(7).In(team).Send("here")
	moved := team.MigrateToSupergroup(-1001)

	reply := callForm(t, k, "sendMessage", map[string]string{"chat_id": "-42", "text": "hello"})
	if reply.OK || !strings.Contains(reply.Description, "upgraded to a supergroup") {
		t.Fatalf("reply = %+v, want the refusal a stale id gets", reply)
	}
	if reply.Parameters.MigrateToChatID != -1001 {
		t.Errorf("migrate_to_chat_id = %d, want the refusal to name the new chat", reply.Parameters.MigrateToChatID)
	}

	if members := moved.Members(); len(members) != 1 || members[0].ID() != 7 {
		t.Errorf("members = %v, want them carried over", members)
	}
	if reply := callForm(t, k, "sendMessage", map[string]string{"chat_id": "-1001", "text": "hello"}); !reply.OK {
		t.Errorf("reply = %+v, want the supergroup open for business", reply)
	}

	seen := got.all()
	if len(seen) != 3 || seen[1].Message.MigrateToChatID != -1001 || seen[2].Message.MigrateFromChatID != -42 {
		t.Errorf("updates = %+v, want the move written in both chats", seen)
	}
}

func TestABlockedBotIsTurnedAway(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)
	k.Fail(Blocked(), ToUser(ada))

	reply := callForm(t, k, "sendMessage", map[string]string{"chat_id": "7", "text": "hello"})
	if reply.status != 403 || !strings.Contains(reply.Description, "blocked by the user") {
		t.Errorf("reply = %+v, want Telegram's forbidden", reply)
	}
}

func TestAChatReportsWhatIsPinned(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	team := k.Group(-42, "Standup")
	k.User(7).In(team).Send("read this")
	callForm(t, k, "pinChatMessage", map[string]string{
		"chat_id": "-42", "message_id": fmt.Sprint(team.History()[0].ID),
	})

	var info models.ChatFullInfo
	callForm(t, k, "getChat", map[string]string{"chat_id": "-42"}).decode(t, &info)
	if info.PinnedMessage == nil || info.PinnedMessage.Text != "read this" {
		t.Errorf("pinned = %+v, want the message the bot pinned", info.PinnedMessage)
	}
}

func TestDeletingAPinnedMessageTakesTheOneBefore(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	team := k.Group(-42, "Standup")
	alan := k.User(7).In(team)
	alan.Send("first")
	alan.Send("second")

	said := team.History()
	for _, m := range said {
		callForm(t, k, "pinChatMessage", map[string]string{"chat_id": "-42", "message_id": fmt.Sprint(m.ID)})
	}
	callForm(t, k, "deleteMessage", map[string]string{"chat_id": "-42", "message_id": fmt.Sprint(said[1].ID)})

	pinned, ok := team.Pinned()
	if !ok || pinned.Text != "first" {
		t.Errorf("pinned = %+v, %v; want the pin under the deleted one", pinned, ok)
	}
}

func TestAMigratedGroupHandsOverStandingsOfItsOwn(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	team := k.Group(-42, "Standup")
	k.User(7).In(team).Join()
	moved := team.MigrateToSupergroup(-1042)

	if reply := callForm(t, k, "banChatMember", map[string]string{
		"chat_id": "-1042", "user_id": "7",
	}); !reply.OK {
		t.Fatalf("reply = %+v, want the ban allowed", reply)
	}
	if members := moved.Members(); len(members) != 0 {
		t.Errorf("supergroup members = %v, want the banned one gone", members)
	}
	if members := team.Members(); len(members) != 1 {
		t.Errorf("group members = %v, want the chat it left behind untouched", members)
	}
}

// The pin is written in the chat, but what the bot does is not a reply the
// member has to read.
func TestAPinIsNotAReplyToRead(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	team := k.Group(-42, "Standup")
	alan := k.User(7).In(team)
	alan.Send("read this")
	callForm(t, k, "pinChatMessage", map[string]string{
		"chat_id": "-42", "message_id": fmt.Sprint(team.History()[0].ID),
	})

	alan.ExpectNothingMore()
}

// Pinning, reacting and closing a poll are acts in a chat, so the chat comes
// first: whether the bot may be there at all, then whatever right it holds.
func TestTheBotActsOnlyWhereItCanReach(t *testing.T) {
	k := talking(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	b := newClient(t, k)
	ctx := context.Background()

	card := mustSend(t, b, "hello")
	poll, err := b.SendPoll(ctx, &bot.SendPollParams{
		ChatID: testChatID, Question: "tea?", Options: []models.InputPollOption{{Text: "yes"}, {Text: "no"}},
	})
	if err != nil {
		t.Fatalf("SendPoll: %v", err)
	}
	k.User(testChatID).BlockBot()

	chat := fmt.Sprint(testChatID)
	for _, one := range []struct {
		name   string
		method string
		form   map[string]string
	}{
		{"pin", "pinChatMessage", map[string]string{"chat_id": chat, "message_id": fmt.Sprint(card.ID)}},
		{"unpin", "unpinChatMessage", map[string]string{"chat_id": chat}},
		{"react", "setMessageReaction", map[string]string{
			"chat_id": chat, "message_id": fmt.Sprint(card.ID),
			"reaction": `[{"type":"emoji","emoji":"\U0001F44D"}]`,
		}},
		{"stop the poll", "stopPoll", map[string]string{"chat_id": chat, "message_id": fmt.Sprint(poll.ID)}},
	} {
		reply := callForm(t, k, one.method, one.form)
		if reply.OK || reply.status != http.StatusForbidden || !strings.Contains(reply.Description, "blocked by the user") {
			t.Errorf("%s in a blocked chat = %+v, want it forbidden", one.name, reply)
		}
	}

	// Deleting is the one thing a blocked chat still allows: a bot clearing up
	// after a block is ordinary, and Telegram documents nothing against it.
	if reply := callForm(t, k, "deleteMessage", map[string]string{
		"chat_id": chat, "message_id": fmt.Sprint(card.ID),
	}); !reply.OK {
		t.Errorf("delete in a blocked chat = %+v, want it through", reply)
	}

	// Out of a group, the refusal is the standing rather than the right.
	team := k.Group(-42, "Standup")
	k.User(9).In(team).Send("morning")
	mine := fmt.Sprint(team.History()[0].ID)
	k.User(9).In(team).RemoveBot()

	reply := callForm(t, k, "pinChatMessage", map[string]string{"chat_id": "-42", "message_id": mine})
	if reply.OK || reply.status != http.StatusForbidden || !strings.Contains(reply.Description, "kicked from the group chat") {
		t.Errorf("pin after being kicked = %+v, want the standing, not the right", reply)
	}
	gone := callForm(t, k, "deleteMessage", map[string]string{"chat_id": "-42", "message_id": mine})
	if gone.OK || gone.status != http.StatusForbidden || !strings.Contains(gone.Description, "kicked from the group chat") {
		t.Errorf("delete after being kicked = %+v, want it forbidden", gone)
	}
}

// A file id is looked at after the chat, so a bot writing where it may not is
// told that rather than told about the file.
func TestTheChatIsReadBeforeTheFile(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	stranger := k.User(7)

	reply := sendMedia(t, k, stranger.ID(), "sendPhoto", "photo", "photo", "never-issued")
	if reply.OK || reply.status != http.StatusForbidden || !strings.Contains(reply.Description, "initiate conversation") {
		t.Errorf("reply = %+v, want the chat refused before the file", reply)
	}
}
