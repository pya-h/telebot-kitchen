package kitchen

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	runRushes = flag.Bool("kitchen.stress", false, "run the rushes, which an ordinary test run skips")
	rushSeed  = flag.Int64("kitchen.seed", 0, "the seed to replay a rush with")
	rushOrder = flag.Int("kitchen.order", 0, "replay only this one order of a rush")
)

const (
	defaultOrders      = 100
	defaultConcurrency = 8
	defaultOrderTime   = 10 * time.Second
	defaultSeed        = 1
)

// Rush runs many short conversations at once, hunting the failures that only
// show up under concurrency: a reply in the wrong chat, a reply that never
// arrives, one that arrives twice, a verb that never settles. Races are not the
// target — the race detector already finds those, with a better stack trace.
//
// Every order gets a kitchen of its own, so memory is bounded by how many run at
// once rather than by how many run in total. An ordinary test run skips a rush
// entirely; -kitchen.stress turns it on.
type Rush struct {
	Orders      int                  // conversations to run
	Concurrency int                  // how many at once; sockets, not memory, are the ceiling
	Seed        int64                // what Order.Rand runs from, and what replays a failure
	Timeout     time.Duration        // how long one order may take before it counts as stuck
	Options     []Option             // what every kitchen in the rush is built with
	Bot         func(*Kitchen) error // build the bot and bind it, once per kitchen
	Serve       func(*Order)         // the conversation, written with the ordinary verbs
}

// Order is one conversation in a rush, with the whole kitchen surface on it.
type Order struct {
	*Kitchen

	// Ticket belongs to this order and no other. Put it in what the users say:
	// the rush reads every chat afterwards looking for somebody else's ticket,
	// which is how a reply landing in the wrong conversation is caught.
	Ticket string

	// N is the order's number, and what -kitchen.order replays.
	N int

	// Rand is seeded from the rush's seed and this number, so a script that
	// varies itself varies the same way when the failure is replayed.
	Rand *rand.Rand
}

func (r Rush) Run(tb TB) {
	if !*runRushes {
		return
	}
	if r.Bot == nil || r.Serve == nil {
		tb.Errorf("kitchen: a rush needs both a Bot to build and an Order to Serve")
		return
	}

	seed, orders, concurrency := r.settings()
	first, last := 1, orders
	// A replay is one order on its own, which is the only way to read it.
	if *rushOrder > 0 {
		first, last, concurrency = *rushOrder, *rushOrder, 1
	}

	var (
		mu     sync.Mutex
		broken []brokenOrder
	)
	slots := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for n := first; n <= last; n++ {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()

			if errs := r.serveOne(n, seed); len(errs) > 0 {
				mu.Lock()
				defer mu.Unlock()
				broken = append(broken, brokenOrder{n: n, errs: errs})
			}
		}()
	}
	wg.Wait()

	r.report(tb, broken, last-first+1, concurrency, seed)
}

func (r Rush) settings() (seed int64, orders, concurrency int) {
	seed, orders, concurrency = r.Seed, r.Orders, r.Concurrency
	if *rushSeed != 0 {
		seed = *rushSeed
	}
	if seed == 0 {
		seed = defaultSeed
	}
	if orders <= 0 {
		orders = defaultOrders
	}
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	return seed, orders, concurrency
}

func (r Rush) serveOne(n int, seed int64) []string {
	tb := &orderTB{}
	k := New(tb, r.Options...)
	if err := r.Bot(k); err != nil {
		tb.Errorf("kitchen: build the bot: %v", err)
		tb.close()
		return tb.errors()
	}

	order := &Order{
		Kitchen: k,
		Ticket:  ticketOf(n),
		N:       n,
		Rand:    rand.New(rand.NewPCG(uint64(seed), uint64(n))),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				tb.Errorf("kitchen: the order panicked: %v", p)
			}
		}()
		r.Serve(order)
	}()

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultOrderTime
	}
	stuck := time.NewTimer(timeout)
	defer stuck.Stop()

	select {
	case <-done:
		order.checkTickets(tb)
		tb.close()
	case <-stuck.C:
		// The script is still in there, and tearing its kitchen down would block
		// on whatever it is stuck in, so this one is left standing.
		tb.Errorf("kitchen: the order was still working after %s, so a verb never settled", timeout)
	}
	return tb.errors()
}

var ticketPattern = regexp.MustCompile(`ticket-\d+`)

func ticketOf(n int) string { return "ticket-" + strconv.Itoa(n) }

// Each order has a kitchen to itself, so another order's ticket can only have
// arrived through state the bot shares between them — which is exactly the bug
// that puts one person's reply in somebody else's chat.
func (o *Order) checkTickets(tb TB) {
	for _, chatID := range o.world.chatIDs() {
		for _, m := range o.History(chatID) {
			for _, found := range ticketPattern.FindAllString(m.Text, -1) {
				if found != o.Ticket {
					tb.Errorf("kitchen: chat %d was told %q, which belongs to another conversation", chatID, m)
				}
			}
		}
	}
}

type brokenOrder struct {
	n    int
	errs []string
}

func (r Rush) report(tb TB, broken []brokenOrder, ran, concurrency int, seed int64) {
	if len(broken) == 0 {
		return
	}
	slices.SortFunc(broken, func(a, b brokenOrder) int { return a.n - b.n })

	var out strings.Builder
	fmt.Fprintf(&out, "kitchen: %d of %d orders broke at %d-way concurrency\n", len(broken), ran, concurrency)
	for _, o := range broken {
		fmt.Fprintf(&out, "\n  order %d:\n", o.n)
		for _, err := range o.errs {
			out.WriteString("    " + strings.ReplaceAll(err, "\n", "\n    ") + "\n")
		}
	}
	fmt.Fprintf(&out, "\n  replay it: go test -run <this test> -kitchen.stress -kitchen.seed=%d -kitchen.order=%d",
		seed, broken[0].n)

	tb.Errorf("%s", out.String())
}

// orderTB collects an order's failures instead of failing the run, so a rush
// reports every broken order rather than only the first one to notice.
type orderTB struct {
	mu       sync.Mutex
	errs     []string
	cleanups []func()
}

func (o *orderTB) Cleanup(f func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cleanups = append(o.cleanups, f)
}

func (o *orderTB) Errorf(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.errs = append(o.errs, fmt.Sprintf(format, args...))
}

func (o *orderTB) Failed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.errs) > 0
}

func (o *orderTB) errors() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.errs)
}

func (o *orderTB) close() {
	o.mu.Lock()
	cleanups := o.cleanups
	o.cleanups = nil
	o.mu.Unlock()

	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
}
