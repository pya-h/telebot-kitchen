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

func TestAGroupMessageCarriesTheGroup(t *testing.T) {
	k := New(t)
	var got *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { got = u.Message })

	team := k.Group(-42, "Standup")
	k.User(7, WithFullName("Ada", "Lovelace")).In(team).Send("morning")

	if got.Chat.ID != -42 || got.Chat.Type != models.ChatTypeGroup || got.Chat.Title != "Standup" {
		t.Errorf("chat = %+v, want the group", got.Chat)
	}
	// A private chat is its user; a shared one has a sender of its own.
	if got.From == nil || got.From.ID != 7 || got.Chat.FirstName != "" {
		t.Errorf("from = %+v, chat = %+v; want the member as sender and no identity on the group", got.From, got.Chat)
	}
}

func TestEachChatKeepsItsOwnPlace(t *testing.T) {
	k := New(t)
	k.DeliverTo(syncBot(t, k, echoHandler).ProcessUpdate)

	team := k.Supergroup(-1001, "Team")
	alan := k.User(7)

	alan.Send("private")
	alan.In(team).Send("shared")

	if reply := alan.Expect(TextIs("echo: private")); reply.ChatID != 7 {
		t.Errorf("reply in chat %d, want the private one", reply.ChatID)
	}
	if reply := alan.In(team).Expect(TextIs("echo: shared")); reply.ChatID != -1001 {
		t.Errorf("reply in chat %d, want the group", reply.ChatID)
	}
	alan.ExpectNothingMore()
	alan.In(team).ExpectNothingMore()
}

// Two people in one group read the same reply, each on their own watermark.
func TestEveryMemberReadsWhatTheBotSaid(t *testing.T) {
	k := New(t)
	k.DeliverTo(syncBot(t, k, echoHandler).ProcessUpdate)

	team := k.Group(-42, "Standup")
	alan, sara := k.User(7).In(team), k.User(9).In(team)

	alan.Send("hello")
	alan.Expect(TextIs("echo: hello"))
	sara.Expect(TextIs("echo: hello"))
}

func TestSpeakingPutsAMemberOnTheRoster(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "Standup")

	k.User(9).In(team).Send("morning")
	k.User(7).In(team).Send("morning")

	members := team.Members()
	if len(members) != 2 || members[0].ID() != 7 || members[1].ID() != 9 {
		t.Errorf("members = %v, want both in id order", members)
	}
	if k.User(11).In(k.Group(-43, "Other")).Send("elsewhere"); len(team.Members()) != 2 {
		t.Errorf("members = %v, want another group's to stay out", team.Members())
	}
}

func TestASharedChatIsRefusedAPositiveID(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	New(tb).Group(42, "Standup")

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "must be negative") {
		t.Errorf("errors = %v, want one about the id", errs)
	}
}

func TestAChannelCarriesPostsRatherThanTalk(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	news := k.Channel(-1002, "Releases")
	k.User(7).In(news).Send("hello")

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "cannot speak") {
		t.Fatalf("errors = %v, want one about speaking in a channel", errs)
	}
	if log := news.History(); len(log) != 0 {
		t.Errorf("history = %v, want nothing said", log)
	}
}

func TestSubscribersReadWhatTheBotPosts(t *testing.T) {
	k := New(t)
	b := syncBot(t, k, echoHandler)
	k.DeliverTo(b.ProcessUpdate)

	news := k.Channel(-1002, "Releases")
	if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID: news.ID(), Text: "v1 is out",
	}); err != nil {
		t.Fatalf("post: %v", err)
	}

	k.User(7).In(news).Expect(TextIs("v1 is out"))
	if news.Title() != "Releases" {
		t.Errorf("title = %q, want the channel's", news.Title())
	}
}

// A group with no title is still named, so a transcript never reads as blank.
func TestAnUntitledChatIsNamedAfterItsID(t *testing.T) {
	if title := New(t).Group(-42, "").Title(); title != "Chat42" {
		t.Errorf("title = %q, want a default naming the id", title)
	}
}

func TestAChannelPostReachesTheBotAsAPost(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)

	news := k.Channel(-1002, "Releases")
	post := news.Post("v1 is out")
	news.EditPost(post, "v1.0.1 is out")

	seen := got.all()
	if len(seen) != 2 || seen[0].ChannelPost == nil || seen[1].EditedChannelPost == nil {
		t.Fatalf("updates = %+v, want the post and the edit, neither of them a message", seen)
	}
	published := seen[0].ChannelPost
	if published.From != nil || published.SenderChat == nil || published.SenderChat.ID != -1002 {
		t.Errorf("post = %+v, want the channel itself as the sender", published)
	}
	if edited := seen[1].EditedChannelPost; edited.Text != "v1.0.1 is out" || edited.ID != post.ID {
		t.Errorf("edit = %+v, want the same post reworded", edited)
	}
}

func TestOnlyAChannelCarriesPosts(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	New(tb).Group(-42, "Standup").Post("v1 is out")

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "only a channel carries posts") {
		t.Errorf("errors = %v, want one about posting to a group", errs)
	}
}

func TestSpeakingBringsBackSomebodyWhoLeft(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	team := k.Group(-42, "Standup")
	alan := k.User(7).In(team)
	alan.Join()
	alan.Leave()
	alan.Send("one more thing")

	if members := team.Members(); len(members) != 1 || members[0].ID() != 7 {
		t.Errorf("members = %v, want whoever is talking to be in the room", members)
	}
}

func TestAnIDKeepsTheKindItWasFirstGiven(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.Group(-42, "Standup")
	again := k.Channel(-42, "Releases")

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "already a group") {
		t.Fatalf("errors = %v, want one about the kind already taken", errs)
	}
	if again.kind != models.ChatTypeGroup {
		t.Errorf("handle is a %s, want it to agree with the chat that exists", again.kind)
	}
}

// A migration that is refused must not half-happen: the chat it was asked to
// become has no business existing, and the handle should name the real one.
func TestAGroupMigratesOnlyOnce(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "Standup")
	k.User(7).In(team).Send("hi")

	moved := team.MigrateToSupergroup(-1042)
	again := team.MigrateToSupergroup(-1099)

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "already migrated to -1042") {
		t.Errorf("errors = %v, want one naming where it went", errs)
	}
	if again.ID() != moved.ID() {
		t.Errorf("second migration returned %d, want the supergroup it became", again.ID())
	}
	if _, registered := k.world.info(-1099); registered {
		t.Error("the refused migration registered a chat nothing became")
	}
}

func TestAPublicChatAnswersToItsUsername(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	news := k.Channel(-1001, "News").Public("@news_room")
	ada := k.User(7, Started())
	ada.In(news).Join()

	var info models.ChatFullInfo
	callForm(t, k, "getChat", map[string]string{"chat_id": "@News_Room"}).decode(t, &info)
	if info.ID != news.ID() || info.Username != "news_room" {
		t.Errorf("chat = %+v, want the channel behind the name, case and all", info)
	}

	var named, numbered models.ChatMember
	callForm(t, k, "getChatMember", map[string]string{"chat_id": "@news_room", "user_id": "7"}).decode(t, &named)
	callForm(t, k, "getChatMember", map[string]string{"chat_id": "-1001", "user_id": "7"}).decode(t, &numbered)
	if named.Type != numbered.Type || named.Member == nil || named.Member.User.ID != 7 {
		t.Errorf("by name = %+v, by id = %+v; want the same answer", named, numbered)
	}

	if reply := callForm(t, k, "sendMessage", map[string]string{"chat_id": "@news_room", "text": "hello"}); !reply.OK {
		t.Errorf("reply = %+v, want a post by name accepted", reply)
	}
	posted := k.History(news.ID())
	if len(posted) != 2 || posted[1].Text != "hello" {
		t.Fatalf("history = %+v, want the post landed in the channel", posted)
	}

	// The chat a call reads from is named the same way.
	copied := callForm(t, k, "copyMessage", map[string]string{
		"chat_id": fmt.Sprint(ada.ID()), "from_chat_id": "@news_room",
		"message_id": fmt.Sprint(posted[1].ID),
	})
	if !copied.OK {
		t.Errorf("copy = %+v, want the source named by username", copied)
	}

	// A name nobody holds, and a name that belongs to a person rather than a chat.
	k.User(8, WithUsername("ada"))
	for _, name := range []string{"@nobody", "@ada"} {
		reply := callForm(t, k, "getChat", map[string]string{"chat_id": name})
		if reply.OK || reply.status != http.StatusBadRequest || !strings.Contains(reply.Description, "chat not found") {
			t.Errorf("%s = %+v, want chat not found", name, reply)
		}
	}
}

func TestOnlyASupergroupOrChannelIsPublic(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.Group(-42, "Standup").Public("standup")

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "no public username") {
		t.Fatalf("errors = %q, want one about the kind of chat", errs)
	}
	if id, known := k.world.byUsername("standup"); known {
		t.Errorf("@standup answers for chat %d, want a refused name to have been taken by nobody", id)
	}

	// Saying it twice for the same chat is saying the same thing.
	k.Channel(-1001, "News").Public("news_room").Public("news_room")
	if errs := tb.errors(); len(errs) != 1 {
		t.Errorf("errors = %q, want nothing said about a chat keeping its own name", errs)
	}
}

// A call that names its chat is the same call as one that numbers it: the record
// and the matchers see the id either way.
func TestACallByNameIsRecordedByID(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	news := k.Channel(-1001, "News").Public("news_room")
	k.Fail(Refuse(http.StatusBadRequest, "Bad Request: nope"), ToChat(news.ID()), Method("sendMessage"))

	reply := callForm(t, k, "sendMessage", map[string]string{"chat_id": "@news_room", "text": "hi"})
	if reply.OK || !strings.Contains(reply.Description, "nope") {
		t.Errorf("reply = %+v, want the fault scoped to the chat to fire for its name", reply)
	}
	if call := k.Expect(Method("sendMessage")); call.ChatID != news.ID() {
		t.Errorf("recorded chat = %d, want %d", call.ChatID, news.ID())
	}
}

func TestAUsernameAnswersForOneChat(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.Channel(-1001, "News").Public("news_room")
	k.Supergroup(-1002, "News talk").Public("@News_Room")

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "already answers") {
		t.Fatalf("errors = %q, want one about the name being taken", errs)
	}
	if id, known := k.world.byUsername("news_room"); !known || id != -1001 {
		t.Errorf("@news_room = %d, %v; want the chat that had it first", id, known)
	}
}
