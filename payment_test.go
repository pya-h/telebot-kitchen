package kitchen

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const boost = "boost:30d"

func invoiceFor(t *testing.T, b *bot.Bot, chatID int64) {
	t.Helper()
	if _, err := b.SendInvoice(context.Background(), &bot.SendInvoiceParams{
		ChatID:      chatID,
		Title:       "Boost",
		Description: "Thirty days at the top",
		Payload:     boost,
		Currency:    "XTR",
		Prices:      []models.LabeledPrice{{Label: "Boost", Amount: 100}},
	}); err != nil {
		t.Fatalf("SendInvoice: %v", err)
	}
}

// checkoutBot approves or refuses whatever it is asked to charge for, and keeps
// every update it was handed, since half of what a payment must do is arrive.
func checkoutBot(t *testing.T, k *Kitchen, approve bool) (*bot.Bot, *updates) {
	t.Helper()
	seen := &updates{}
	b := syncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		seen.mu.Lock()
		seen.seen = append(seen.seen, *u)
		seen.mu.Unlock()
		if u.PreCheckoutQuery == nil {
			return
		}
		b.AnswerPreCheckoutQuery(ctx, &bot.AnswerPreCheckoutQueryParams{
			PreCheckoutQueryID: u.PreCheckoutQuery.ID,
			OK:                 approve,
			ErrorMessage:       "sold out",
		})
	})
	return b, seen
}

func TestNothingIsChargedUntilTheBotApproves(t *testing.T) {
	k := New(t)
	b, seen := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)
	ada := k.User(7)

	invoiceFor(t, b, ada.ChatID())
	paid, ok := ada.Pay()

	if !ok || paid.Payload != boost || paid.Amount != 100 || paid.Currency != "XTR" {
		t.Fatalf("payment = %+v %v, want the invoice charged", paid, ok)
	}
	if ledger := k.Payments(); len(ledger) != 1 || ledger[0].ChargeID != paid.ChargeID {
		t.Errorf("ledger = %v, want the one charge", ledger)
	}

	// The bot has to be told, or it has nothing to hand the grant out on.
	told := seen.all()
	last := told[len(told)-1].Message
	if last == nil || last.SuccessfulPayment == nil {
		t.Fatalf("updates = %+v, want the payment to reach the bot", told)
	}
	if got := last.SuccessfulPayment; got.TelegramPaymentChargeID != paid.ChargeID ||
		got.InvoicePayload != boost || got.TotalAmount != 100 {
		t.Errorf("successful_payment = %+v, want the charge it stands for", got)
	}
}

func TestARefusedCheckoutChargesNothing(t *testing.T) {
	k := New(t)
	b, _ := checkoutBot(t, k, false)
	k.DeliverTo(b.ProcessUpdate)
	ada := k.User(7)

	invoiceFor(t, b, ada.ChatID())
	if paid, ok := ada.Pay(); ok {
		t.Fatalf("payment = %+v, want nothing charged", paid)
	}
	if ledger := k.Payments(); len(ledger) != 0 {
		t.Errorf("ledger = %v, want it empty", ledger)
	}
	k.Expect(Method("answerPreCheckoutQuery"), Param("error_message", "sold out"))
}

func TestABotThatNeverAnswersChargesNothing(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)

	invoiceFor(t, b, ada.ChatID())
	if _, ok := ada.Pay(); ok {
		t.Fatal("payment went through with nobody approving it")
	}
	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "never answered") {
		t.Errorf("errors = %v, want one about the unanswered query", errs)
	}
}

// The exploit the refund path exists to catch: a grant handed out on a charge
// that is later taken back has to be revocable, so the charge must say so.
func TestARefundRevokesTheChargeItGaveBack(t *testing.T) {
	k := New(t)
	b, _ := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)
	ada := k.User(7)

	invoiceFor(t, b, ada.ChatID())
	paid, _ := ada.Pay()

	if _, err := b.RefundStarPayment(context.Background(), &bot.RefundStarPaymentParams{
		UserID: ada.ID(), TelegramPaymentChargeID: paid.ChargeID,
	}); err != nil {
		t.Fatalf("RefundStarPayment: %v", err)
	}

	ledger := k.Payments()
	if len(ledger) != 1 || !ledger[0].Refunded {
		t.Errorf("ledger = %v, want the charge marked given back", ledger)
	}
	if last := ada.History()[len(ada.History())-1]; last.Event != "refunded" {
		t.Errorf("history = %v, want the refund recorded", ada.History())
	}
}

// A refund is the bot's own doing, so it comes back as no update — the same
// rule its own message, edit and pin follow.
func TestARefundIsNotAnUpdate(t *testing.T) {
	k := New(t)
	b, seen := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)
	ada := k.User(7)

	invoiceFor(t, b, ada.ChatID())
	paid, _ := ada.Pay()
	before := len(seen.all())

	b.RefundStarPayment(context.Background(), &bot.RefundStarPaymentParams{
		UserID: ada.ID(), TelegramPaymentChargeID: paid.ChargeID,
	})
	k.Settle()

	if after := seen.all(); len(after) != before {
		t.Errorf("updates = %+v, want the bot told nothing about its own refund", after[before:])
	}
	ada.ExpectNothingMore()
}

func TestAChargeIsGivenBackOnlyOnce(t *testing.T) {
	k := New(t)
	b, _ := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)
	ada := k.User(7)

	invoiceFor(t, b, ada.ChatID())
	paid, _ := ada.Pay()
	refund := map[string]string{
		"user_id": fmt.Sprint(ada.ID()), "telegram_payment_charge_id": paid.ChargeID,
	}

	callForm(t, k, "refundStarPayment", refund)
	if reply := callForm(t, k, "refundStarPayment", refund); reply.OK ||
		!strings.Contains(reply.Description, "ALREADY_REFUNDED") {
		t.Errorf("reply = %+v, want the second refund refused", reply)
	}
}

func TestARefundNeedsTheUserWhoPaid(t *testing.T) {
	k := New(t)
	b, _ := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)
	ada, grace := k.User(7), k.User(8)

	invoiceFor(t, b, ada.ChatID())
	paid, _ := ada.Pay()

	reply := callForm(t, k, "refundStarPayment", map[string]string{
		"user_id": fmt.Sprint(grace.ID()), "telegram_payment_charge_id": paid.ChargeID,
	})
	if reply.OK || !strings.Contains(reply.Description, "CHARGE_NOT_FOUND") {
		t.Errorf("reply = %+v, want somebody else's charge to be none of theirs", reply)
	}
	if k.Payments()[0].Refunded {
		t.Error("the charge was given back to the wrong person")
	}
}

// The library reads a transaction partner from a flat object and cannot write
// one, so the shape the kitchen sends has to survive the bot's own decoder.
func TestTheStarLedgerReadsBackThroughTheLibrary(t *testing.T) {
	k := New(t)
	b, _ := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)
	ada := k.User(7, WithFullName("Ada", "Lovelace"))

	invoiceFor(t, b, ada.ChatID())
	paid, _ := ada.Pay()
	b.RefundStarPayment(context.Background(), &bot.RefundStarPaymentParams{
		UserID: ada.ID(), TelegramPaymentChargeID: paid.ChargeID,
	})

	ledger, err := b.GetStarTransactions(context.Background(), &bot.GetStarTransactionsParams{})
	if err != nil {
		t.Fatalf("GetStarTransactions: %v", err)
	}
	if len(ledger.Transactions) != 2 {
		t.Fatalf("transactions = %+v, want the charge and the refund", ledger.Transactions)
	}

	// Newest first, so the refund leads.
	back, charge := ledger.Transactions[0], ledger.Transactions[1]
	if back.Amount != -100 || back.Receiver == nil || back.Receiver.User == nil {
		t.Errorf("refund = %+v, want it going back to a user", back)
	}
	if charge.Amount != 100 || charge.Source == nil || charge.Source.User == nil {
		t.Errorf("charge = %+v, want it coming from a user", charge)
	}
	if charge.Source.User.User.FirstName != "Ada" || charge.Source.User.InvoicePayload != boost {
		t.Errorf("payer = %+v, want Ada and what she bought", charge.Source.User)
	}
}

func TestPayingWithNothingToPayForReports(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	k.User(7).Pay()

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "no invoice") {
		t.Errorf("errors = %v, want one about there being nothing to pay", errs)
	}
}

func TestACheckoutQueryNobodyIssuedIsRefused(t *testing.T) {
	k := New(t)
	for _, id := range []string{"", "checkout-999"} {
		reply := callForm(t, k, "answerPreCheckoutQuery", map[string]string{
			"pre_checkout_query_id": id, "ok": "true",
		})
		if reply.OK {
			t.Errorf("reply = %+v, want %q refused", reply, id)
		}
	}
}

// Telegram takes one answer per query; a bot answering twice is a bug worth
// seeing rather than a second charge worth taking.
func TestACheckoutQueryIsAnsweredOnce(t *testing.T) {
	k := New(t)
	var second error
	b := syncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		if u.PreCheckoutQuery == nil {
			return
		}
		answer := &bot.AnswerPreCheckoutQueryParams{PreCheckoutQueryID: u.PreCheckoutQuery.ID, OK: true}
		b.AnswerPreCheckoutQuery(ctx, answer)
		_, second = b.AnswerPreCheckoutQuery(ctx, answer)
	})
	k.DeliverTo(b.ProcessUpdate)
	ada := k.User(7)

	invoiceFor(t, b, ada.ChatID())
	if _, ok := ada.Pay(); !ok {
		t.Fatal("the first answer did not go through")
	}
	if second == nil || !strings.Contains(second.Error(), "query ID is invalid") {
		t.Errorf("second answer = %v, want it refused", second)
	}
	if ledger := k.Payments(); len(ledger) != 1 {
		t.Errorf("ledger = %v, want one charge", ledger)
	}
}

func TestAnInvoiceReadsAsItsTitle(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	invoiceFor(t, b, testChatID)

	if want := "**Kitchen:** (invoice) Boost\n"; k.Transcript(testChatID) != want {
		t.Errorf("transcript = %q, want %q", k.Transcript(testChatID), want)
	}
}

func TestAnInvoiceNeedsWhatTelegramAsksFor(t *testing.T) {
	k := New(t)
	full := map[string]string{
		"chat_id": fmt.Sprint(testChatID), "title": "Boost", "description": "d",
		"payload": boost, "currency": "XTR", "prices": `[{"label":"Boost","amount":100}]`,
	}
	for _, missing := range []string{"title", "description", "payload", "currency", "prices"} {
		short := map[string]string{}
		for name, value := range full {
			if name != missing {
				short[name] = value
			}
		}
		if reply := callForm(t, k, "sendInvoice", short); reply.OK ||
			!strings.Contains(reply.Description, missing) {
			t.Errorf("without %s = %+v, want a refusal naming it", missing, reply)
		}
	}
}
