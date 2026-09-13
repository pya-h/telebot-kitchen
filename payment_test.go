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
	ada := k.User(7, Started())

	invoiceFor(t, b, ada.ChatID())
	paid, ok := ada.Pay()

	if !ok || paid.Payload != boost || paid.Amount != 100 || paid.Currency != "XTR" {
		t.Fatalf("payment = %+v %v, want the invoice charged", paid, ok)
	}
	if ledger := k.Payments(); len(ledger) != 1 || ledger[0].ChargeID != paid.ChargeID {
		t.Errorf("ledger = %v, want the one charge", ledger)
	}
	// Ids count per kind, so the first charge of a run is always the first.
	if paid.ChargeID != "charge-1" {
		t.Errorf("charge id = %q, want it counted from one", paid.ChargeID)
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
	ada := k.User(7, Started())

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
	ada := k.User(7, Started())

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
	ada := k.User(7, Started())

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
	ada := k.User(7, Started())

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
	ada := k.User(7, Started())

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
	ada, grace := k.User(7, Started()), k.User(8)

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
	ada := k.User(7, WithFullName("Ada", "Lovelace"), Started())

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
	ada := k.User(7, Started())

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
	k := talking(t)
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

// A bot that says something while approving the checkout has still said it: the
// payment landing afterwards must not step over the reply.
func TestAReplySentDuringCheckoutIsStillRead(t *testing.T) {
	k := New(t)
	b := syncBot(t, k, func(ctx context.Context, c *bot.Bot, u *models.Update) {
		if u.PreCheckoutQuery == nil {
			return
		}
		c.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: u.PreCheckoutQuery.From.ID, Text: "one moment",
		})
		c.AnswerPreCheckoutQuery(ctx, &bot.AnswerPreCheckoutQueryParams{
			PreCheckoutQueryID: u.PreCheckoutQuery.ID, OK: true,
		})
	})
	k.DeliverTo(b.ProcessUpdate)
	ada := k.User(7, Started())

	invoiceFor(t, b, ada.ChatID())
	if _, ok := ada.Pay(); !ok {
		t.Fatal("the payment did not go through")
	}
	ada.Expect(TextIs("one moment"))
	ada.ExpectNothingMore()
}

// The refund belongs where the charge was taken, not in whatever chat shares the
// payer's id: a bot selling in a group has to show the money going back there.
func TestARefundLandsWhereTheChargeWasTaken(t *testing.T) {
	k := New(t)
	b, _ := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)
	team := k.Group(-42, "Standup")
	ada := k.User(7)

	invoiceFor(t, b, team.ID())
	paid, ok := ada.In(team).Pay()
	if !ok || paid.ChatID != team.ID() {
		t.Fatalf("payment = %+v %v, want it charged in the group", paid, ok)
	}

	if _, err := b.RefundStarPayment(context.Background(), &bot.RefundStarPaymentParams{
		UserID: ada.ID(), TelegramPaymentChargeID: paid.ChargeID,
	}); err != nil {
		t.Fatalf("RefundStarPayment: %v", err)
	}

	if last := team.History(); last[len(last)-1].Event != "refunded" {
		t.Errorf("group = %v, want the refund recorded there", last)
	}
	if aside := k.History(ada.ID()); len(aside) != 0 {
		t.Errorf("private chat = %v, want the refund nowhere near it", aside)
	}
}

func TestNobodyPaysInAChannel(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	news := k.Channel(-1002, "Releases")
	k.User(7).In(news).Pay()

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "cannot pay in a channel") {
		t.Errorf("errors = %v, want one about a channel", errs)
	}
	if ledger := k.Payments(); len(ledger) != 0 {
		t.Errorf("ledger = %v, want it empty", ledger)
	}
}

func TestAStarsInvoiceIsTheOneShapeTelegramTakes(t *testing.T) {
	k := talking(t)
	full := map[string]string{
		"chat_id": fmt.Sprint(testChatID), "title": "Boost", "description": "d",
		"payload": boost, "currency": "XTR", "prices": `[{"label":"Boost","amount":100}]`,
	}
	wrong := []struct {
		name    string
		field   string
		value   string
		refused string
	}{
		{"a provider behind Stars", "provider_token", "tok", "provider_token must be empty"},
		{"a price broken down", "prices", `[{"label":"Boost","amount":60},{"label":"Tax","amount":40}]`, "exactly one item"},
		{"nothing to charge", "prices", `[{"label":"Boost","amount":0}]`, "amount must be positive"},
	}
	for _, one := range wrong {
		asked := map[string]string{"reply_markup": `{"keyboard":[["Pay"]]}`}
		for name, value := range full {
			asked[name] = value
		}
		asked[one.field] = one.value

		reply := callForm(t, k, "sendInvoice", asked)
		if reply.OK || reply.ErrorCode != http.StatusBadRequest || !strings.Contains(reply.Description, one.refused) {
			t.Errorf("%s = %+v, want a refusal saying %q", one.name, reply, one.refused)
		}
	}
	if log := k.History(testChatID); len(log) != 0 {
		t.Errorf("chat holds %+v, want no invoice from a refused call", log)
	}
	if menu := k.User(testChatID).Menu(); len(menu) != 0 {
		t.Errorf("menu = %v, want a refused invoice to have raised no keyboard", menu)
	}

	// The flags Telegram ignores for Stars are taken rather than refused.
	full["need_name"], full["need_email"], full["need_phone_number"] = "true", "true", "true"
	if reply := callForm(t, k, "sendInvoice", full); !reply.OK {
		t.Fatalf("reply = %+v, want the one shape Telegram takes accepted", reply)
	}

	b, _ := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)
	paid, ok := k.User(testChatID).Pay()
	if !ok || paid.Amount != 100 || paid.Currency != "XTR" {
		t.Errorf("paid = %+v, %v; want the invoice paid as before", paid, ok)
	}
}

// A currency with a provider behind it is not held to the Stars rules.
func TestAnInvoiceInAnotherCurrencyKeepsItsBreakdown(t *testing.T) {
	k := talking(t)
	reply := callForm(t, k, "sendInvoice", map[string]string{
		"chat_id": fmt.Sprint(testChatID), "title": "Boost", "description": "d",
		"payload": boost, "currency": "EUR", "provider_token": "tok",
		"prices": `[{"label":"Boost","amount":900},{"label":"VAT","amount":100}]`,
	})
	if !reply.OK {
		t.Fatalf("reply = %+v, want it accepted", reply)
	}
	var sent models.Message
	reply.decode(t, &sent)
	if sent.Invoice == nil || sent.Invoice.TotalAmount != 1000 {
		t.Errorf("invoice = %+v, want the prices summed", sent.Invoice)
	}
}

// Telegram may hand an update over twice, and a bot that grants on a payment
// must not grant twice for it.
func TestARedeliveredUpdateIsTheSameUpdate(t *testing.T) {
	k := talking(t)
	b, seen := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)
	invoiceFor(t, b, testChatID)

	paid, ok := k.User(testChatID).Pay()
	if !ok {
		t.Fatalf("Pay = %+v, %v; want the charge through", paid, ok)
	}
	k.Redeliver()

	var payments []models.Update
	for _, u := range seen.all() {
		if u.Message != nil && u.Message.SuccessfulPayment != nil {
			payments = append(payments, u)
		}
	}
	if len(payments) != 2 {
		t.Fatalf("payment updates = %+v, want the one update handed over twice", payments)
	}
	first, again := payments[0], payments[1]
	if again.ID != first.ID || again.Message.ID != first.Message.ID ||
		again.Message.SuccessfulPayment.TelegramPaymentChargeID != paid.ChargeID {
		t.Errorf("redelivered = %+v, want the first update again, id and all", again)
	}
	if charges := k.Payments(); len(charges) != 1 {
		t.Errorf("payments = %+v, want the redelivery to have charged nothing", charges)
	}
}

// A currency with a provider behind it needs the provider's token, and no
// currency Telegram cannot name is taken at all.
func TestAnInvoiceInAnotherCurrencyNeedsItsProvider(t *testing.T) {
	k := talking(t)
	full := map[string]string{
		"chat_id": fmt.Sprint(testChatID), "title": "Boost", "description": "d",
		"payload": boost, "currency": "EUR", "provider_token": "tok",
		"prices": `[{"label":"Boost","amount":900}]`,
	}
	for _, one := range []struct {
		name    string
		field   string
		value   string
		refused string
	}{
		{"no provider token", "provider_token", "", "PAYMENT_PROVIDER_INVALID"},
		{"a currency in lower case", "currency", "eur", "CURRENCY_INVALID"},
		{"a currency of the wrong shape", "currency", "EURO", "CURRENCY_INVALID"},
	} {
		asked := map[string]string{}
		for name, value := range full {
			asked[name] = value
		}
		asked[one.field] = one.value

		reply := callForm(t, k, "sendInvoice", asked)
		if reply.OK || reply.ErrorCode != http.StatusBadRequest || !strings.Contains(reply.Description, one.refused) {
			t.Errorf("%s = %+v, want %s", one.name, reply, one.refused)
		}
	}
}

// refundStarPayment is for Stars. A provider's charge is refunded through the
// provider, and is not in the Stars ledger at all.
func TestOnlyAStarsChargeIsGivenBackThroughTelegram(t *testing.T) {
	k := talking(t)
	b, _ := checkoutBot(t, k, true)
	k.DeliverTo(b.ProcessUpdate)

	callForm(t, k, "sendInvoice", map[string]string{
		"chat_id": fmt.Sprint(testChatID), "title": "Boost", "description": "d",
		"payload": boost, "currency": "EUR", "provider_token": "tok",
		"prices": `[{"label":"Boost","amount":900}]`,
	})
	paid, ok := k.User(testChatID).Pay()
	if !ok || paid.Currency != "EUR" {
		t.Fatalf("paid = %+v, %v; want the provider's charge", paid, ok)
	}

	reply := callForm(t, k, "refundStarPayment", map[string]string{
		"user_id": fmt.Sprint(testChatID), "telegram_payment_charge_id": paid.ChargeID,
	})
	if reply.OK || !strings.Contains(reply.Description, "CHARGE_NOT_FOUND") {
		t.Errorf("refund = %+v, want a charge Stars never took to be unknown", reply)
	}

	var ledger struct {
		Transactions []struct {
			ID string `json:"id"`
		} `json:"transactions"`
	}
	callForm(t, k, "getStarTransactions", nil).decode(t, &ledger)
	if len(ledger.Transactions) != 0 {
		t.Errorf("star transactions = %+v, want the provider's charge kept out", ledger.Transactions)
	}
}
