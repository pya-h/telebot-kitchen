package kitchen

import (
	"context"
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
	k.User(7, WithFullName("Ali", "Rezaei")).In(team).Send("morning")

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
	ali := k.User(7)

	ali.Send("private")
	ali.In(team).Send("shared")

	if reply := ali.Expect(TextIs("echo: private")); reply.ChatID != 7 {
		t.Errorf("reply in chat %d, want the private one", reply.ChatID)
	}
	if reply := ali.In(team).Expect(TextIs("echo: shared")); reply.ChatID != -1001 {
		t.Errorf("reply in chat %d, want the group", reply.ChatID)
	}
	ali.ExpectNothingMore()
	ali.In(team).ExpectNothingMore()
}

// Two people in one group read the same reply, each on their own watermark.
func TestEveryMemberReadsWhatTheBotSaid(t *testing.T) {
	k := New(t)
	k.DeliverTo(syncBot(t, k, echoHandler).ProcessUpdate)

	team := k.Group(-42, "Standup")
	ali, sara := k.User(7).In(team), k.User(9).In(team)

	ali.Send("hello")
	ali.Expect(TextIs("echo: hello"))
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
	ali := k.User(7).In(team)
	ali.Join()
	ali.Leave()
	ali.Send("one more thing")

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
