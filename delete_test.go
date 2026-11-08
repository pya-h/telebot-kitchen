package kitchen

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// deleter is a kitchen whose bot echoes, with one user who has said something and
// been answered: the two messages every delete rule is about.
func deleter(t *testing.T) (k *Kitchen, ada *User, theirs, own Message) {
	t.Helper()
	k = New(t)
	k.DeliverTo(syncBot(t, k, echoHandler).ProcessUpdate)
	ada = k.User(7)
	ada.Send("hello")
	own = ada.Expect(TextIs("echo: hello"))
	return k, ada, k.History(7)[0], own
}

func deleteOne(t *testing.T, k *Kitchen, chatID int64, id int) apiReply {
	t.Helper()
	return callForm(t, k, "deleteMessage", map[string]string{
		"chat_id": fmt.Sprint(chatID), "message_id": fmt.Sprint(id),
	})
}

func deleteMany(t *testing.T, k *Kitchen, chatID int64, ids ...int) apiReply {
	t.Helper()
	return deleteList(t, k, chatID, strings.ReplaceAll(fmt.Sprint(ids), " ", ","))
}

func deleteList(t *testing.T, k *Kitchen, chatID int64, list string) apiReply {
	t.Helper()
	return callForm(t, k, "deleteMessages", map[string]string{
		"chat_id": fmt.Sprint(chatID), "message_ids": list,
	})
}

func stillThere(k *Kitchen, chatID int64) []int {
	var ids []int
	for _, m := range k.History(chatID) {
		ids = append(ids, m.ID)
	}
	return ids
}

func TestADeleteIsHeldToFortyEightHours(t *testing.T) {
	const justUnder = 48*time.Hour - time.Minute

	t.Run("47h59m", func(t *testing.T) {
		k, _, theirs, own := deleter(t)
		k.Clock().Advance(justUnder)
		for _, id := range []int{theirs.ID, own.ID} {
			if reply := deleteOne(t, k, 7, id); !reply.OK {
				t.Errorf("reply = %+v, want a message under 48 hours deleted", reply)
			}
		}
	})
	t.Run("48h00m", func(t *testing.T) {
		k, _, theirs, own := deleter(t)
		k.Clock().Advance(48 * time.Hour)
		for _, id := range []int{theirs.ID, own.ID} {
			reply := deleteOne(t, k, 7, id)
			if reply.OK || reply.ErrorCode != 400 || !strings.Contains(reply.Description, "can't be deleted for everyone") {
				t.Errorf("reply = %+v, want a 400", reply)
			}
		}
		if got := stillThere(k, 7); len(got) != 2 {
			t.Errorf("left %v, want both messages still there", got)
		}
	})
}

func TestTooOldReadsAsTelegramSaysItInEachKindOfChat(t *testing.T) {
	cases := map[string]struct {
		chat func(*Kitchen) *Chat
		want string
	}{
		"group":      {func(k *Kitchen) *Chat { return k.Group(-42, "G") }, "Bad Request: message can't be deleted for everyone"},
		"supergroup": {func(k *Kitchen) *Chat { return k.Supergroup(-43, "S") }, "Bad Request: message can't be deleted"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			k := New(t)
			k.DeliverTo(func(context.Context, *models.Update) {})
			chat := c.chat(k)
			k.User(7).In(chat).Send("old")
			k.User(9).In(chat).PromoteBot(DeleteMessages)
			k.Clock().Advance(48 * time.Hour)

			reply := deleteOne(t, k, chat.id, chat.History()[0].ID)
			if reply.ErrorCode != 400 || reply.Description != c.want {
				t.Errorf("reply = %+v, want %q", reply, c.want)
			}
		})
	}
}

func TestAPrivateDiceWaitsADay(t *testing.T) {
	cases := map[string]struct {
		age  time.Duration
		goes bool
	}{
		"just rolled":  {0, false},
		"23h59m":       {24*time.Hour - time.Minute, false},
		"a day":        {24 * time.Hour, true},
		"47h59m":       {48*time.Hour - time.Minute, true},
		"two days old": {48 * time.Hour, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			k := New(t)
			k.DeliverTo(func(context.Context, *models.Update) {})
			ada := k.User(7)
			ada.RollDice("🎲", 3)
			k.Clock().Advance(c.age)

			reply := deleteOne(t, k, 7, k.History(7)[0].ID)
			if reply.OK != c.goes || (!c.goes && reply.ErrorCode != 400) {
				t.Errorf("reply = %+v, want deleted = %v", reply, c.goes)
			}
		})
	}
}

func TestADiceInAGroupNeedsNoWaiting(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "G")
	k.User(7).In(team).RollDice("🎲", 3)
	k.User(9).In(team).PromoteBot(DeleteMessages)

	if reply := deleteOne(t, k, -42, team.History()[0].ID); !reply.OK {
		t.Errorf("reply = %+v, want the rule kept to private chats", reply)
	}
}

func TestDeleteMessagesTakesWhatItFindsAndSkipsTheRest(t *testing.T) {
	k, ada, theirs, own := deleter(t)
	ada.Send("stays")
	stays := k.History(7)[2].ID

	reply := deleteMany(t, k, 7, theirs.ID, own.ID, 999)
	if !reply.OK || string(reply.Result) != "true" {
		t.Fatalf("reply = %+v, want true", reply)
	}
	if got := stillThere(k, 7); !slices.Contains(got, stays) || slices.Contains(got, theirs.ID) || slices.Contains(got, own.ID) {
		t.Errorf("left %v, want the two deleted and the rest kept", got)
	}
	if ada.Screen().Text == "hello" {
		t.Error("the screen still shows a deleted message")
	}
	if !k.Calls().Has(Method("deleteMessages"), ToChat(7)) {
		t.Errorf("the call is not on the record:\n%s", k.Calls())
	}
}

func TestDeleteMessagesThroughTheLibrary(t *testing.T) {
	k, _, theirs, own := deleter(t)
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()))
	if err != nil {
		t.Fatal(err)
	}

	done, err := b.DeleteMessages(context.Background(), &bot.DeleteMessagesParams{ChatID: 7, MessageIDs: []int{theirs.ID, own.ID}})
	if err != nil || !done {
		t.Fatalf("done = %v, err = %v", done, err)
	}
	if got := stillThere(k, 7); len(got) != 0 {
		t.Errorf("left %v, want both deleted", got)
	}
}

func TestDeleteMessagesSizes(t *testing.T) {
	ids := func(n int) string {
		list := make([]string, n)
		for i := range list {
			list[i] = fmt.Sprint(i + 1000)
		}
		return "[" + strings.Join(list, ",") + "]"
	}
	cases := map[string]struct {
		list string
		ok   bool
		want string
	}{
		"empty, as the server takes it": {"[]", true, ""},
		"one":                           {ids(1), true, ""},
		"a hundred":                     {ids(100), true, ""},
		"a hundred and one":             {ids(101), false, "too many message identifiers specified"},
		"absent":                        {"", false, "message identifiers are not specified"},
		"not JSON":                      {"[1,", false, "can't parse message_ids JSON object"},
		"not a list":                    {"7", false, "expected an Array of message identifiers"},
		"a thing in the list":           {"[{}]", false, "message identifier must be a Number"},
		"not a whole number":            {"[1.5]", false, "can't parse message identifier as a Number"},
		"not a number at all":           {`["x"]`, false, "can't parse message identifier as a Number"},
		"zero":                          {"[0]", false, "invalid message identifier specified"},
		"a number as text":              {`["1000"]`, true, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			k, _, _, _ := deleter(t)
			reply := deleteList(t, k, 7, c.list)
			if reply.OK != c.ok || (!c.ok && (reply.ErrorCode != 400 || reply.Description != "Bad Request: "+c.want)) {
				t.Errorf("reply = %+v, want ok = %v %q", reply, c.ok, c.want)
			}
		})
	}
}

func TestAnEmptyListDeletesNothing(t *testing.T) {
	k, _, _, _ := deleter(t)
	if reply := deleteList(t, k, 7, "[]"); !reply.OK {
		t.Fatalf("reply = %+v", reply)
	}
	if got := stillThere(k, 7); len(got) != 2 {
		t.Errorf("left %v, want everything", got)
	}
}

func TestNoneFoundIsStillTrue(t *testing.T) {
	k, _, _, _ := deleter(t)
	if reply := deleteMany(t, k, 7, 998, 999); !reply.OK || string(reply.Result) != "true" {
		t.Errorf("reply = %+v, want true", reply)
	}
}

func TestOneMessageTooOldRefusesTheWholeBatch(t *testing.T) {
	k, ada, old, _ := deleter(t)
	k.Clock().Advance(48 * time.Hour)
	ada.Send("fresh")
	fresh := k.History(7)[2].ID

	reply := deleteMany(t, k, 7, fresh, old.ID)
	if reply.OK || reply.ErrorCode != 400 {
		t.Fatalf("reply = %+v, want the batch refused", reply)
	}
	if got := stillThere(k, 7); !slices.Contains(got, fresh) || !slices.Contains(got, old.ID) {
		t.Errorf("left %v, want nothing deleted", got)
	}
}

func TestAFreshDiceRefusesTheWholeBatch(t *testing.T) {
	k, ada, theirs, _ := deleter(t)
	ada.RollDice("🎲", 3)
	dice := k.History(7)[2].ID

	if reply := deleteMany(t, k, 7, theirs.ID, dice); reply.OK || reply.ErrorCode != 400 {
		t.Fatalf("reply = %+v, want the batch refused", reply)
	}
	if got := stillThere(k, 7); !slices.Contains(got, theirs.ID) {
		t.Errorf("left %v, want nothing deleted", got)
	}
}

func TestABatchIsHeldToTheBotsRights(t *testing.T) {
	k := New(t)
	k.DeliverTo(syncBot(t, k, echoHandler).ProcessUpdate)
	team := k.Group(-42, "G")
	alan := k.User(7).In(team)
	alan.Send("hello")
	own := alan.Expect(TextIs("echo: hello"))
	theirs := team.History()[0]
	k.User(9).In(team).PromoteBot(PinMessages)

	reply := deleteMany(t, k, -42, own.ID, theirs.ID)
	if reply.OK || reply.ErrorCode != 400 || !strings.Contains(reply.Description, "can't be deleted") {
		t.Fatalf("reply = %+v, want somebody else's message refused", reply)
	}
	if got := stillThere(k, -42); len(got) != 2 {
		t.Errorf("left %v, want the bot's own kept too", got)
	}
}

func TestDeletingABatchUnpinsWhatItTakes(t *testing.T) {
	k, _, theirs, _ := deleter(t)
	if reply := callForm(t, k, "pinChatMessage", map[string]string{"chat_id": "7", "message_id": fmt.Sprint(theirs.ID)}); !reply.OK {
		t.Fatalf("pin: %+v", reply)
	}
	deleteMany(t, k, 7, theirs.ID)

	if pinned := k.world.chats[7].pinned; len(pinned) != 0 {
		t.Errorf("still pinned: %v", pinned)
	}
}

func TestDeletingABatchTellsTheBotNothing(t *testing.T) {
	k := New(t)
	heard := 0
	k.DeliverTo(func(context.Context, *models.Update) { heard++ })
	ada := k.User(7)
	ada.Send("hello")

	deleteMany(t, k, 7, k.History(7)[0].ID)
	if heard != 1 {
		t.Errorf("the bot heard %d updates, want only the message", heard)
	}
}

func TestABlockedChatCanStillBeClearedInABatch(t *testing.T) {
	k, ada, theirs, _ := deleter(t)
	ada.BlockBot()

	if reply := deleteMany(t, k, 7, theirs.ID); !reply.OK {
		t.Errorf("reply = %+v, want a blocked chat treated as deleteMessage treats it", reply)
	}
}

func TestABatchInAChatThatDoesNotExist(t *testing.T) {
	k := New(t)
	if reply := deleteMany(t, k, 12345, 1); reply.OK || !strings.Contains(reply.Description, "chat not found") {
		t.Errorf("reply = %+v, want chat not found", reply)
	}
}
