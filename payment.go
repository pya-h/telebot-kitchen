package kitchen

import (
	"fmt"
	"sync"

	"github.com/go-telegram/bot/models"
)

// Payment is a Stars charge that went through, and whether the bot has since
// given it back. A grant a refund should revoke is tested against Refunded.
type Payment struct {
	ChargeID string
	ChatID   int64
	UserID   int64
	Payload  string
	Amount   int
	Currency string
	Refunded bool
}

// invoice is what a bot asked for. Telegram's Invoice object carries no
// payload, so the kitchen keeps its own record of what a message would charge.
type invoice struct {
	chatID   int64
	payload  string
	currency string
	amount   int
}

type checkout struct {
	invoice  invoice
	user     models.User
	ok       bool
	answered bool
}

// entry is one line of the Stars ledger: a charge in, or a refund back out.
type entry struct {
	id      string
	amount  int
	date    int
	user    models.User
	payload string
	back    bool
}

type ledger struct {
	mu        sync.Mutex
	checkouts int64
	refunds   int64
	invoices  []invoice
	pending   map[string]*checkout
	charges   []*Payment
	byCharge  map[string]*Payment
	entries   []entry
}

func newLedger() *ledger {
	return &ledger{pending: map[string]*checkout{}, byCharge: map[string]*Payment{}}
}

func (l *ledger) bill(inv invoice) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.invoices = append(l.invoices, inv)
}

func (l *ledger) newest(chatID int64) (invoice, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for i := len(l.invoices) - 1; i >= 0; i-- {
		if l.invoices[i].chatID == chatID {
			return l.invoices[i], true
		}
	}
	return invoice{}, false
}

func (l *ledger) ask(inv invoice, who models.User) string {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.checkouts++
	id := fmt.Sprintf("checkout-%d", l.checkouts)
	l.pending[id] = &checkout{invoice: inv, user: who}
	return id
}

func (l *ledger) answer(queryID string, ok bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	c, waiting := l.pending[queryID]
	if !waiting || c.answered {
		return false
	}
	c.ok, c.answered = ok, true
	return true
}

func (l *ledger) answered(queryID string) (checkout, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	c, waiting := l.pending[queryID]
	if !waiting || !c.answered {
		return checkout{}, false
	}
	return *c, true
}

func (l *ledger) charge(c checkout, at int) Payment {
	l.mu.Lock()
	defer l.mu.Unlock()

	paid := &Payment{
		ChargeID: fmt.Sprintf("charge-%d", len(l.charges)+1),
		ChatID:   c.invoice.chatID,
		UserID:   c.user.ID,
		Payload:  c.invoice.payload,
		Amount:   c.invoice.amount,
		Currency: c.invoice.currency,
	}
	l.charges = append(l.charges, paid)
	l.byCharge[paid.ChargeID] = paid
	l.entries = append(l.entries, entry{id: paid.ChargeID, amount: paid.Amount, date: at, user: c.user, payload: paid.Payload})
	return *paid
}

func (l *ledger) refund(userID int64, chargeID string, at int, who models.User) (Payment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	paid, found := l.byCharge[chargeID]
	if !found || paid.UserID != userID {
		return Payment{}, requestError("CHARGE_NOT_FOUND")
	}
	if paid.Refunded {
		return Payment{}, requestError("CHARGE_ALREADY_REFUNDED")
	}
	paid.Refunded = true
	l.refunds++
	l.entries = append(l.entries, entry{
		id: fmt.Sprintf("refund-%d", l.refunds), amount: -paid.Amount, date: at,
		user: who, payload: paid.Payload, back: true,
	})
	return *paid, nil
}

// The library reads a transaction partner from a flat object but has no
// MarshalJSON to write one back, so the kitchen puts the wire shape together.
type starTransaction struct {
	ID       string       `json:"id"`
	Amount   int          `json:"amount"`
	Date     int          `json:"date"`
	Source   *starPartner `json:"source,omitempty"`
	Receiver *starPartner `json:"receiver,omitempty"`
}

type starPartner struct {
	Type            string      `json:"type"`
	TransactionType string      `json:"transaction_type"`
	User            models.User `json:"user"`
	InvoicePayload  string      `json:"invoice_payload,omitempty"`
}

// transactions pages the ledger newest first, the way Telegram hands it over.
func (l *ledger) transactions(offset, limit int) any {
	l.mu.Lock()
	defer l.mu.Unlock()

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	out := make([]starTransaction, 0, limit)
	for i := len(l.entries) - 1 - offset; i >= 0 && len(out) < limit; i-- {
		e := l.entries[i]
		party := &starPartner{
			Type: "user", TransactionType: "invoice_payment",
			User: e.user, InvoicePayload: e.payload,
		}
		t := starTransaction{ID: e.id, Amount: e.amount, Date: e.date}
		if e.back {
			t.Receiver = party
		} else {
			t.Source = party
		}
		out = append(out, t)
	}
	return struct {
		Transactions []starTransaction `json:"transactions"`
	}{out}
}

func (l *ledger) all() []Payment {
	l.mu.Lock()
	defer l.mu.Unlock()

	paid := make([]Payment, len(l.charges))
	for i, p := range l.charges {
		paid[i] = *p
	}
	return paid
}

// Payments is every Stars charge the bot has taken, oldest first, each saying
// whether it has since been given back.
func (k *Kitchen) Payments() []Payment { return k.payments.all() }

func (k *Kitchen) sendInvoice(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"title", "description", "payload", "currency"} {
		if p[name] == "" {
			return nil, badRequest(name)
		}
	}
	var prices []models.LabeledPrice
	if err := p.decode("prices", &prices); err != nil || len(prices) == 0 {
		return nil, badRequest("prices")
	}
	markup, err := k.accept(p, chatID)
	if err != nil {
		return nil, err
	}

	amount := 0
	for _, price := range prices {
		amount += price.Amount
	}
	sender := k.botUser()
	sent := k.world.add(chatID, models.Message{
		From:        &sender,
		ReplyMarkup: markup,
		Invoice: &models.Invoice{
			Title:       p["title"],
			Description: p["description"],
			Currency:    p["currency"],
			TotalAmount: amount,
		},
	})
	k.payments.bill(invoice{
		chatID: chatID, payload: p["payload"], currency: p["currency"], amount: amount,
	})
	return sent, nil
}

func (k *Kitchen) answerPreCheckoutQuery(p params) (any, error) {
	id := p["pre_checkout_query_id"]
	if id == "" {
		return nil, badRequest("pre_checkout_query_id")
	}
	if !k.payments.answer(id, p.flag("ok")) {
		return nil, requestError("query is too old and response timeout expired or query ID is invalid")
	}
	return true, nil
}

func (k *Kitchen) refundStarPayment(p params) (any, error) {
	userID, err := p.chat("user_id")
	if err != nil {
		return nil, err
	}
	chargeID := p["telegram_payment_charge_id"]
	if chargeID == "" {
		return nil, badRequest("telegram_payment_charge_id")
	}

	who, known := k.knownUser(userID)
	if !known {
		return nil, requestError("USER_NOT_FOUND")
	}
	paid, err := k.payments.refund(userID, chargeID, int(k.clock.Now().Unix()), who)
	if err != nil {
		return nil, err
	}

	// The chat records it, but no update comes back: what the bot does is never
	// delivered to it, the same as its own pin.
	k.world.add(paid.ChatID, models.Message{RefundedPayment: &models.RefundedPayment{
		Currency:                paid.Currency,
		TotalAmount:             paid.Amount,
		InvoicePayload:          paid.Payload,
		TelegramPaymentChargeID: paid.ChargeID,
	}})
	return true, nil
}

func (k *Kitchen) getStarTransactions(p params) (any, error) {
	return k.payments.transactions(p.number("offset"), p.number("limit")), nil
}

// Pay is the member going through with the newest invoice in the chat. The bot
// gets a pre-checkout query first and the money only moves if it approves,
// which is the order Telegram uses and the order a grant has to be written in.
func (m *Member) Pay() (Payment, bool) {
	k := m.kitchen()
	if m.chat.kind == models.ChatTypeChannel {
		k.tb.Errorf("kitchen: %s cannot pay in a channel, where nothing they do becomes a message", m)
		return Payment{}, false
	}
	if m.shutOut() {
		return Payment{}, false
	}

	inv, billed := k.payments.newest(m.chat.id)
	if !billed {
		k.tb.Errorf("kitchen: %s has no invoice to pay", m)
		return Payment{}, false
	}

	m.awaitFromNow()
	sender := m.user.identity()
	query := k.payments.ask(inv, sender)
	k.deliver(models.Update{PreCheckoutQuery: &models.PreCheckoutQuery{
		ID:             query,
		From:           &sender,
		Currency:       inv.currency,
		TotalAmount:    inv.amount,
		InvoicePayload: inv.payload,
	}})
	k.Settle()

	answer, answered := k.payments.answered(query)
	if !answered {
		k.tb.Errorf("kitchen: the bot never answered %s's pre-checkout query, so nothing was charged", m)
		return Payment{}, false
	}
	if !answer.ok {
		return Payment{}, false
	}

	paid := k.payments.charge(answer, int(k.clock.Now().Unix()))
	// No watermark move: the anchor was set before the checkout, and anything
	// the bot said while approving is still a reply waiting to be read.
	sent := k.world.add(m.chat.id, models.Message{
		From: &sender,
		SuccessfulPayment: &models.SuccessfulPayment{
			Currency:                paid.Currency,
			TotalAmount:             paid.Amount,
			InvoicePayload:          paid.Payload,
			TelegramPaymentChargeID: paid.ChargeID,
		},
	})
	k.deliver(models.Update{Message: &sent})
	return paid, true
}
