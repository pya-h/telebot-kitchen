package kitchen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestAReactionReachesTheBotWithWhatItReplaced(t *testing.T) {
	k := New(t, alsoHearing("message_reaction"))
	b := newClient(t, k)
	var seen []*models.MessageReactionUpdated
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.MessageReaction != nil {
			seen = append(seen, u.MessageReaction)
		}
	})
	ada := k.User(7, Started())

	if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: ada.ChatID(), Text: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	ada.React("👍")
	ada.React("🔥")
	ada.React()
	k.Settle()

	if len(seen) != 3 {
		t.Fatalf("reactions = %d, want one per change", len(seen))
	}
	if len(seen[0].OldReaction) != 0 || seen[0].NewReaction[0].ReactionTypeEmoji.Emoji != "👍" {
		t.Errorf("first = %+v, want it to replace nothing", seen[0])
	}
	if seen[1].OldReaction[0].ReactionTypeEmoji.Emoji != "👍" || seen[1].NewReaction[0].ReactionTypeEmoji.Emoji != "🔥" {
		t.Errorf("second = %+v, want the one it replaced named", seen[1])
	}
	if len(seen[2].NewReaction) != 0 {
		t.Errorf("third = %+v, want it to clear the reaction", seen[2])
	}
	if seen[0].User == nil || seen[0].User.ID != 7 {
		t.Errorf("reaction = %+v, want ada named", seen[0])
	}
}

func TestAChatShowsWhatIsReactedToIt(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "Standup")
	ada, bob := k.User(7).In(team), k.User(8).In(team)
	ada.Join()
	bob.Join()

	if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: team.ID(), Text: "shipped"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent := latestIn(t, k, team.ID())
	ada.React("👍")
	bob.React("👍")
	k.Settle()

	if _, err := b.SetMessageReaction(context.Background(), &bot.SetMessageReactionParams{
		ChatID: team.ID(), MessageID: sent, Reaction: []models.ReactionType{emojiReaction("🎉")},
	}); err != nil {
		t.Fatalf("react: %v", err)
	}

	shown := messageAt(t, k, team.ID(), sent)
	if strings.Join(shown.Reactions, " ") != "👍 🎉" {
		t.Errorf("reactions = %v, want each one once, the bot's included", shown.Reactions)
	}
	if !strings.Contains(shown.String(), "\n👍 🎉") {
		t.Errorf("rendered = %q, want the reactions under the message", shown.String())
	}
}

func TestTheBotIsNotToldAboutItsOwnReaction(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	heard := 0
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.MessageReaction != nil || u.MessageReactionCount != nil {
			heard++
		}
	})
	ada := k.User(7)
	ada.Send("hello")
	k.Settle()
	sent := latestIn(t, k, ada.ChatID())

	if _, err := b.SetMessageReaction(context.Background(), &bot.SetMessageReactionParams{
		ChatID: ada.ChatID(), MessageID: sent, Reaction: []models.ReactionType{emojiReaction("👍")},
	}); err != nil {
		t.Fatalf("react: %v", err)
	}
	k.Settle()

	if heard != 0 {
		t.Errorf("updates = %d, want the bot told nothing about its own doing", heard)
	}
	if shown := messageAt(t, k, ada.ChatID(), sent); strings.Join(shown.Reactions, "") != "👍" {
		t.Errorf("reactions = %v, want the bot's reaction on the message anyway", shown.Reactions)
	}
}

func TestAGroupTellsOnlyABotThatAdministersIt(t *testing.T) {
	k := New(t, alsoHearing("message_reaction"))
	b := newClient(t, k)
	heard := 0
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.MessageReaction != nil {
			heard++
		}
	})
	team := k.Group(-42, "Standup")
	ada := k.User(7).In(team)
	ada.Join()

	if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: team.ID(), Text: "shipped"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	ada.React("👍")
	k.Settle()
	if heard != 1 {
		t.Fatalf("updates = %d, want the reaction heard while the bot administers the chat", heard)
	}

	ada.DemoteBot()
	ada.React("🔥")
	k.Settle()
	if heard != 1 {
		t.Errorf("updates = %d, want no more once the bot is not an administrator", heard)
	}
	// The reaction still lands; only the telling stops.
	if shown := messageAt(t, k, team.ID(), latestIn(t, k, team.ID())); strings.Join(shown.Reactions, "") != "🔥" {
		t.Errorf("reactions = %v, want the reaction on the message regardless", shown.Reactions)
	}
}

func TestAChannelCountsReactionsWithoutNamingAnybody(t *testing.T) {
	k := New(t, alsoHearing("message_reaction_count"))
	k.DeliverTo(func(context.Context, *models.Update) {})
	news := k.Channel(-1001, "News")
	ada, bob := k.User(7).In(news), k.User(8).In(news)

	var counted []*models.MessageReactionCountUpdated
	named := 0
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.MessageReactionCount != nil {
			counted = append(counted, u.MessageReactionCount)
		}
		if u.MessageReaction != nil {
			named++
		}
	})

	news.Post("shipped")
	k.Settle()
	ada.React("👍")
	bob.React("👍")
	k.Settle()

	if named != 0 {
		t.Errorf("named reactions = %d, want a channel to name nobody", named)
	}
	if len(counted) != 2 {
		t.Fatalf("counts = %d, want one per reaction", len(counted))
	}
	last := counted[1]
	if len(last.Reactions) != 1 || last.Reactions[0].TotalCount != 2 {
		t.Errorf("count = %+v, want both counted under the one emoji", last.Reactions)
	}
	if last.Reactions[0].Type.ReactionTypeEmoji.Emoji != "👍" {
		t.Errorf("count = %+v, want it to name the emoji", last.Reactions[0])
	}
}

func TestAReactionSurvivesTheWebhookSeam(t *testing.T) {
	k := New(t, alsoHearing("message_reaction", "message_reaction_count"))
	bodies := make(chan []byte, 4)
	k.DeliverToWebhook(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- body
	}))
	ada := k.User(7)
	ada.Send("hello")
	<-bodies

	ada.React("👍")
	raw := <-bodies

	var got models.Update
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the update did not survive the wire: %v\n%s", err, raw)
	}
	if got.MessageReaction == nil || got.MessageReaction.NewReaction[0].ReactionTypeEmoji.Emoji != "👍" {
		t.Errorf("update = %s, want the reaction readable the other side", raw)
	}
}

func TestAReactionTelegramWouldNotTake(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)
	ada.Send("hello")
	k.Settle()
	sent := latestIn(t, k, ada.ChatID())

	ada.React("🥑")
	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "🥑") {
		t.Errorf("errors = %v, want one naming the reaction Telegram has no room for", errs)
	}

	if _, err := b.SetMessageReaction(context.Background(), &bot.SetMessageReactionParams{
		ChatID: ada.ChatID(), MessageID: sent, Reaction: []models.ReactionType{emojiReaction("🥑")},
	}); err == nil {
		t.Error("the bot reacted with an avocado, want it refused")
	}
	if _, err := b.SetMessageReaction(context.Background(), &bot.SetMessageReactionParams{
		ChatID: ada.ChatID(), MessageID: sent + 99, Reaction: []models.ReactionType{emojiReaction("👍")},
	}); err == nil {
		t.Error("the bot reacted to nothing, want it refused")
	}
}

// A keyboard spells some reactions with a variation selector and Telegram's own
// list does not, so both have to mean the same reaction.
func TestTheSameEmojiSpeltEitherWay(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)
	ada.Send("hello")
	k.Settle()

	ada.React("❤️")
	if shown := messageAt(t, k, ada.ChatID(), latestIn(t, k, ada.ChatID())); len(shown.Reactions) != 1 {
		t.Errorf("reactions = %v, want the heart taken", shown.Reactions)
	}
}

func emojiReaction(emoji string) models.ReactionType {
	return models.ReactionType{
		Type:              models.ReactionTypeTypeEmoji,
		ReactionTypeEmoji: &models.ReactionTypeEmoji{Type: models.ReactionTypeTypeEmoji, Emoji: emoji},
	}
}

func latestIn(t *testing.T, k *Kitchen, chatID int64) int {
	t.Helper()
	sent, said := k.world.latest(chatID)
	if !said {
		t.Fatalf("chat %d has said nothing", chatID)
	}
	return sent.ID
}

func messageAt(t *testing.T, k *Kitchen, chatID int64, messageID int) Message {
	t.Helper()
	for _, m := range k.History(chatID) {
		if m.ID == messageID {
			return m
		}
	}
	t.Fatalf("chat %d has no message %d", chatID, messageID)
	return Message{}
}
