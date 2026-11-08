package kitchen

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type sender func(chatID int64, text string)

// scripted is a bot whose whole behaviour is one function, called once per update.
func scripted(t *testing.T, k *Kitchen, behave func(u *models.Update, send sender)) {
	t.Helper()
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()))
	if err != nil {
		t.Fatal(err)
	}
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		behave(u, func(chatID int64, text string) {
			b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text})
		})
	})
}

func TestNoUpdateIsLostWhenMembersSendAtOnce(t *testing.T) {
	const senders = 8
	for round := range 2000 {
		k := New(&recordingTB{})
		var heard atomic.Int64
		k.DeliverTo(func(context.Context, *models.Update) { heard.Add(1) })

		var wg sync.WaitGroup
		for id := int64(1); id <= senders; id++ {
			member := k.User(id, Started())
			wg.Go(func() { member.Send("hello") })
		}
		wg.Wait()

		if !k.waitFor(func() bool { return heard.Load() == senders }) {
			t.Fatalf("round %d: the bot heard %d of %d updates", round, heard.Load(), senders)
		}
		k.close()
	}
}

func TestAnUpdateQueuedDuringARedeliveryStillArrives(t *testing.T) {
	k := New(t)
	ada, bob := k.User(1, Started()), k.User(2, Started())

	entered, release := make(chan struct{}), make(chan struct{})
	var redelivering atomic.Bool
	var heardBob atomic.Bool
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.Message.From.ID == 2 {
			heardBob.Store(true)
			return
		}
		if redelivering.Load() {
			close(entered)
			<-release
		}
	})

	ada.Send("hello")
	redelivering.Store(true)
	var wg sync.WaitGroup
	wg.Go(ada.Redeliver)
	<-entered
	bob.Send("meanwhile")
	close(release)
	wg.Wait()

	k.WaitFor("bob's update", heardBob.Load)
}

// talkWithoutPause keeps a member speaking until the test is over.
func talkWithoutPause(t *testing.T, m *User) {
	t.Helper()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				m.Send("still here")
			}
		}
	})
	t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})
}

func TestAMemberIsNotHeldUpByAnotherWhoKeepsTalking(t *testing.T) {
	k := New(t, WithWaitTimeout(300*time.Millisecond))
	ada, bob := k.User(1, Started()), k.User(2, Started())
	scripted(t, k, func(u *models.Update, send sender) { send(u.Message.Chat.ID, "echo") })

	ada.Send("hello")
	ada.Expect(TextIs("echo"))
	talkWithoutPause(t, bob)

	ada.ExpectNothingMore()
}

// The control for the test above: the same noise defeats the kitchen-wide wait.
func TestAKitchenWideSettleIsHeldUpByAnyoneTalking(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb, WithWaitTimeout(300*time.Millisecond))
	t.Cleanup(tb.close)
	bob := k.User(2, Started())
	scripted(t, k, func(u *models.Update, send sender) { send(u.Message.Chat.ID, "echo") })

	talkWithoutPause(t, bob)
	k.Settle()

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "nothing settled") {
		t.Errorf("reported %v, want the kitchen-wide wait to give up", errs)
	}
}

func TestExpectNothingMoreCatchesOneReplyTooMany(t *testing.T) {
	cases := map[string]struct {
		extraTo int64
		act     func(ada, bob *User)
	}{
		"to the member's own message": {1, func(ada, _ *User) { ada.Send("twice") }},
		"caused by somebody else":     {1, func(_, bob *User) { bob.Send("twice") }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tb := &recordingTB{}
			k := New(tb)
			t.Cleanup(tb.close)
			ada, bob := k.User(1, Started()), k.User(2, Started())
			scripted(t, k, func(u *models.Update, send sender) {
				if u.Message.Text == "twice" {
					send(c.extraTo, "one too many")
				}
			})

			c.act(ada, bob)
			ada.ExpectNothingMore()

			if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "one too many") {
				t.Errorf("reported %v, want the extra reply caught", errs)
			}
		})
	}
}

// Longer than the quiet period, so only the update being in flight can hold the wait.
func TestAnUpdateAlreadyOnItsWayHoldsEveryMembersWait(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	t.Cleanup(tb.close)
	ada, bob := k.User(1, Started()), k.User(2, Started())

	entered := make(chan struct{})
	scripted(t, k, func(_ *models.Update, send sender) {
		close(entered)
		time.Sleep(3 * quietPeriod)
		send(1, "relayed late")
	})

	var wg sync.WaitGroup
	wg.Go(func() { bob.Send("slow to handle") })
	<-entered
	ada.ExpectNothingMore()
	wg.Wait()

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "relayed late") {
		t.Errorf("reported %v, want the wait held until bob's update was handled", errs)
	}
}

func TestAMembersWaitLastsWhileTheBotWritesToTheirChat(t *testing.T) {
	k := New(t)
	ada := k.User(1, Started())

	var wrote atomic.Int64
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	scripted(t, k, func(_ *models.Update, send sender) {
		wg.Go(func() {
			for range 5 {
				time.Sleep(quietPeriod / 2)
				send(1, "more")
				wrote.Add(1)
			}
		})
	})

	ada.Send("go")
	ada.Settle()

	if got := wrote.Load(); got != 5 {
		t.Errorf("settled after %d of 5 messages, want the chat quiet first", got)
	}
}

func TestAMembersWaitGivesUpOnAChatThatNeverGoesQuiet(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb, WithWaitTimeout(200*time.Millisecond))
	t.Cleanup(tb.close)
	ada := k.User(1, Started())

	stop := make(chan struct{})
	var wg sync.WaitGroup
	scripted(t, k, func(_ *models.Update, send sender) {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				case <-time.After(quietPeriod / 3):
					send(1, "more")
				}
			}
		})
	})

	ada.Send("go")
	ada.Settle()
	close(stop)
	wg.Wait()

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "nothing settled") {
		t.Errorf("reported %v, want the wait to give up and say so", errs)
	}
}

func TestPayingIsNotHeldUpByAnotherWhoKeepsTalking(t *testing.T) {
	k := New(t, WithWaitTimeout(500*time.Millisecond))
	ada, bob := k.User(1, Started()), k.User(2, Started())
	b, _ := checkoutBot(t, k, true)
	invoiceFor(t, b, ada.ChatID())

	talkWithoutPause(t, bob)

	if _, paid := ada.Pay(); !paid {
		t.Error("the payment did not go through while another member was talking")
	}
}

func TestPayingWaitsForAnAnswerSlowerThanTheQuietPeriod(t *testing.T) {
	k := New(t)
	ada := k.User(1, Started())

	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	b := syncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		if u.PreCheckoutQuery == nil {
			return
		}
		wg.Go(func() {
			time.Sleep(3 * quietPeriod)
			b.AnswerPreCheckoutQuery(ctx, &bot.AnswerPreCheckoutQueryParams{PreCheckoutQueryID: u.PreCheckoutQuery.ID, OK: true})
		})
	})
	invoiceFor(t, b, ada.ChatID())

	if _, paid := ada.Pay(); !paid {
		t.Error("gave up on a bot that answered, only slowly")
	}
}

func TestAMemberRedeliversTheirOwnLastUpdate(t *testing.T) {
	k := New(t)
	ada, bob := k.User(1, Started()), k.User(2, Started())

	b, seen := checkoutBot(t, k, true)
	invoiceFor(t, b, ada.ChatID())

	ada.Pay()
	bob.Send("meanwhile")
	before := len(k.History(ada.ChatID()))

	ada.Redeliver()

	seen.mu.Lock()
	defer seen.mu.Unlock()
	heard := seen.seen
	last, payment := heard[len(heard)-1], heard[len(heard)-3]
	if last.Message == nil || last.Message.SuccessfulPayment == nil {
		t.Fatalf("redelivered %+v, want ada's successful_payment rather than bob's message", last)
	}
	if last.ID != payment.ID {
		t.Errorf("redelivered update %d, want the same id as %d", last.ID, payment.ID)
	}
	if after := len(k.History(ada.ChatID())); after != before {
		t.Errorf("the chat went from %d messages to %d, want nothing changed", before, after)
	}
}

func TestKitchenRedeliverStillTakesTheLastOfAnyone(t *testing.T) {
	k := New(t)
	ada, bob := k.User(1, Started()), k.User(2, Started())
	var last atomic.Value
	k.DeliverTo(func(_ context.Context, u *models.Update) { last.Store(u.Message.Text) })

	ada.Send("from ada")
	bob.Send("from bob")
	last.Store("")
	k.Redeliver()

	if got := last.Load(); got != "from bob" {
		t.Errorf("redelivered %q, want the kitchen's last update", got)
	}
}

func TestAMemberWhoSentNothingHasNothingToRedeliver(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	t.Cleanup(tb.close)
	ada, bob := k.User(1, Started()), k.User(2, Started())
	k.DeliverTo(func(context.Context, *models.Update) {})

	ada.Send("hello")
	bob.Redeliver()

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "user 2 has had nothing delivered") {
		t.Errorf("reported %v, want bob told he has nothing to redeliver", errs)
	}
}

func TestAMembersRedeliverStaysInTheirChat(t *testing.T) {
	k := New(t)
	ada := k.User(1, Started())
	room := k.Group(-42, "Room")
	inRoom := ada.In(room)
	inRoom.Join()

	var last atomic.Value
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.Message != nil {
			last.Store(u.Message.Text)
		}
	})

	inRoom.Send("in the room")
	ada.Send("in private")

	inRoom.Redeliver()
	if got := last.Load(); got != "in the room" {
		t.Errorf("redelivered %q, want what they said in that chat", got)
	}
}

func TestWhatHappensOutsideAnyChatCountsForTheMember(t *testing.T) {
	k := New(t)
	ada := k.User(1, Started())
	inRoom := ada.In(k.Group(-42, "Room"))
	inRoom.Join()

	var queries atomic.Int64
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.InlineQuery != nil {
			queries.Add(1)
		}
	})

	inRoom.Send("hello")
	inRoom.Search("cats")
	inRoom.Redeliver()

	if got := queries.Load(); got != 2 {
		t.Errorf("the bot heard %d inline queries, want the search redelivered", got)
	}
}

func TestAMemberCannotRedeliverToAPollingBot(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	t.Cleanup(tb.close)
	ada := k.User(1, Started())
	k.DeliverByPolling()

	ada.Send("hello")
	ada.Redeliver()

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "its next poll is handed it again") {
		t.Errorf("reported %v, want the polling refusal Kitchen.Redeliver gives", errs)
	}
}

func TestAMembersOwnActionSkipsAnUnreadReplyByDefault(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	t.Cleanup(tb.close)
	ada := k.User(1, Started())
	scripted(t, k, func(_ *models.Update, send sender) { send(1, "reply") })

	ada.Send("first")
	k.WaitFor("the first reply", func() bool { return len(k.Calls()) == 1 })
	ada.Send("second")

	ada.Expect(TextIs("reply"))
	ada.ExpectNothingMore()
	if errs := tb.errors(); len(errs) != 0 {
		t.Errorf("reported %v, want the first reply skipped by the second Send", errs)
	}
}

func TestReadsEveryReplyKeepsWhatTheMemberHasNotRead(t *testing.T) {
	skips := map[string]func(k *Kitchen, ada *User){
		"sending":   func(_ *Kitchen, ada *User) { ada.Send("second") },
		"tapping":   func(_ *Kitchen, ada *User) { ada.Tap("go") },
		"searching": func(_ *Kitchen, ada *User) { ada.Search("cats") },
	}
	for name, skip := range skips {
		t.Run(name, func(t *testing.T) {
			k := New(t)
			ada := k.User(1, Started(), ReadsEveryReply())
			var line atomic.Int64
			b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()))
			if err != nil {
				t.Fatal(err)
			}
			k.DeliverTo(func(ctx context.Context, u *models.Update) {
				if u.Message == nil {
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: 1,
					Text:   fmt.Sprintf("reply %d", line.Add(1)),
					ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
						{Text: "go", CallbackData: "go"},
					}}},
				})
			})

			ada.Send("first")
			k.WaitFor("the first reply", func() bool { return len(k.Calls()) == 1 })
			skip(k, ada)

			ada.Expect(TextIs("reply 1"))
		})
	}
}

func TestReadsEveryReplySurvivesBeingMentionedAgain(t *testing.T) {
	k := New(t)
	k.User(1, ReadsEveryReply())
	k.User(1, WithUsername("ada"))
	if !k.readsEveryReply(1) {
		t.Error("mentioning the user again dropped the setting")
	}
}

// Pairs members up and passes on what each says; every chat's replies come from
// somebody else's goroutine, which is the shape that goes wrong.
func relayBot(k *Kitchen) error {
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()))
	if err != nil {
		return err
	}
	partnerOf := func(id int64) int64 {
		if id%2 == 1 {
			return id + 1
		}
		return id - 1
	}
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		switch {
		case u.Message != nil:
			params := &bot.SendMessageParams{ChatID: partnerOf(u.Message.From.ID), Text: u.Message.Text}
			if strings.HasSuffix(u.Message.Text, "last") {
				params.ReplyMarkup = models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
					{Text: "ack", CallbackData: "ack"},
				}}}
			}
			b.SendMessage(ctx, params)
		case u.CallbackQuery != nil:
			b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID})
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: partnerOf(u.CallbackQuery.From.ID),
				Text:   fmt.Sprintf("acked by %d", u.CallbackQuery.From.ID),
			})
		}
	})
	return nil
}

func TestPairsOfMembersRelayingAtOnceKeepEveryLine(t *testing.T) {
	const pairs, lines = 4, 6

	Rush{
		Orders:      60,
		Concurrency: 8,
		Options:     []Option{WithWaitTimeout(8 * time.Second)},
		Timeout:     30 * time.Second,
		Bot:         relayBot,
		Serve: func(o *Order) {
			line := func(id int64, i int) string {
				if i == lines {
					return fmt.Sprintf("%s from %d last", o.Ticket, id)
				}
				return fmt.Sprintf("%s from %d line %d", o.Ticket, id, i)
			}

			var wg sync.WaitGroup
			for id := int64(1); id <= pairs*2; id++ {
				member := o.User(id, Started(), ReadsEveryReply())
				partner := id + 1
				if id%2 == 0 {
					partner = id - 1
				}
				wg.Go(func() {
					for i := 1; i <= lines; i++ {
						member.Send(line(id, i))
						member.Expect(TextIs(line(partner, i)))
					}
					member.Tap("ack")
					member.Expect(TextIs(fmt.Sprintf("acked by %d", partner)))
					member.ExpectNothingMore()
				})
			}
			wg.Wait()

			if sent := o.Calls().Count(Method("sendMessage")); sent != pairs*2*(lines+1) {
				o.Errorf("the bot sent %d messages, want %d", sent, pairs*2*(lines+1))
			}
		},
	}.Run(t)
}
