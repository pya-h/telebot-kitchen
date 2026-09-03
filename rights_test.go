package kitchen

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestAMemberRewordingWhatTheySaidIsAnEdit(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	team := k.Group(-42, "Standup")
	ali := k.User(7).In(team)
	ali.Send("half nine")
	ali.Edit(team.History()[0], "half ten")

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
	ali := k.User(7).In(team)
	ali.Send("hello")
	reply := ali.Expect(TextIs("echo: hello"))
	ali.Edit(reply, "something else")

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
	ali := k.User(7).In(team)
	ali.Send("read this")
	pin := map[string]string{"chat_id": "-42", "message_id": fmt.Sprint(team.History()[0].ID)}

	ali.PromoteBot(PostMessages)
	if reply := callForm(t, k, "pinChatMessage", pin); reply.OK || !strings.Contains(reply.Description, "not enough rights to pin") {
		t.Fatalf("reply = %+v, want the refusal without the right", reply)
	}

	ali.PromoteBot(PinMessages)
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
	ali := k.User(7).In(team)
	ali.Send("first")

	reply := callForm(t, k, "restrictChatMember", map[string]string{
		"chat_id": "-42", "user_id": "7", "permissions": `{"can_send_messages":false}`,
	})
	if !reply.OK {
		t.Fatalf("reply = %+v, want the restriction applied", reply)
	}

	ali.Send("second")
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
	ali := k.User(7).In(team)
	ali.Send("here")
	ali.PromoteBot(PostMessages)

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

	ali.PromoteBot(RestrictMembers, PromoteMembers)
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
	ali := k.User(7).In(team)
	ali.Send("first")
	ali.Send("second")

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
	ali := k.User(7).In(team)
	ali.Send("read this")
	callForm(t, k, "pinChatMessage", map[string]string{
		"chat_id": "-42", "message_id": fmt.Sprint(team.History()[0].ID),
	})

	ali.ExpectNothingMore()
}
