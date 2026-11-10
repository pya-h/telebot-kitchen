package kitchen

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"regexp"
	"runtime"
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

	mostLogLines = 200
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
	Build       func(*Order) error   // instead of Bot, for a bot that must know its order
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

	tb *orderTB
}

func (o *Order) Helper() {}

func (o *Order) Errorf(format string, args ...any) { o.tb.Errorf(format, args...) }

// Fatalf ends the goroutine it is called on, so call it from the script itself.
func (o *Order) Fatalf(format string, args ...any) {
	o.tb.Errorf(format, args...)
	runtime.Goexit()
}

// Logf keeps a line for the report of an order that breaks.
func (o *Order) Logf(format string, args ...any) { o.tb.Logf(format, args...) }

func (o *Order) Failed() bool { return o.tb.Failed() }

func (r Rush) Run(tb TB) {
	if !*runRushes {
		return
	}
	if r.Serve == nil || (r.Bot == nil) == (r.Build == nil) {
		tb.Errorf("kitchen: a rush needs exactly one of Bot and Build, and an Order to Serve")
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

			if order := r.serveOne(n, seed); len(order.errs) > 0 {
				mu.Lock()
				defer mu.Unlock()
				broken = append(broken, order)
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

func (r Rush) serveOne(n int, seed int64) brokenOrder {
	tb := &orderTB{}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultOrderTime
	}

	order := &Order{
		Kitchen: New(tb, append(slices.Clone(r.Options), WithChargeIDs(OrderChargeIDs(n)))...),
		Ticket:  ticketOf(n),
		N:       n,
		Rand:    rand.New(rand.NewPCG(uint64(seed), uint64(n))),
		tb:      tb,
	}

	if r.serve(order, seed, timeout) {
		// After the cleanups, which is when a bot drains what it still had to say.
		tb.tearDown(timeout)
		order.checkTickets(tb)
	}
	errs, logs, dropped := tb.written()
	return brokenOrder{n: n, errs: errs, logs: logs, droppedLogs: dropped}
}

// serve says whether the order got far enough to be taken down; one left stuck
// keeps its kitchen, since tearing that down would block on whatever is stuck.
func (r Rush) serve(o *Order, seed int64, timeout time.Duration) bool {
	if !o.tb.finishes(func() { r.build(o, seed) }, timeout, "kitchen: the bot was still being built after %s") {
		return false
	}
	if o.tb.Failed() {
		return true
	}
	return o.tb.finishes(func() { r.Serve(o) }, timeout, "kitchen: the order was still working after %s, so a verb never settled")
}

func (r Rush) build(o *Order, seed int64) {
	var err error
	if r.Bot != nil {
		err = r.Bot(o.Kitchen)
	} else {
		// A stream of its own, so what the builder draws cannot move the script's.
		building := *o
		building.Rand = rand.New(rand.NewPCG(uint64(seed), ^uint64(o.N)))
		err = r.Build(&building)
	}
	if err != nil {
		o.tb.Errorf("kitchen: build the bot: %v", err)
	}
}

var ticketPattern = regexp.MustCompile(`ticket-\d+`)

// OrderChargeIDs is the charge-id prefix a rush or load run gives order n, so no two orders' payments share an id.
func OrderChargeIDs(n int) string { return "charge-o" + strconv.Itoa(n) }

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
	n           int
	errs        []string
	logs        []string
	droppedLogs int
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
			out.WriteString("    ")
			out.WriteString(strings.ReplaceAll(err, "\n", "\n    "))
			out.WriteString("\n")
		}
		o.reportLogs(&out)
	}
	fmt.Fprintf(&out, "\n  replay it: go test -run <this test> -kitchen.stress -kitchen.seed=%d -kitchen.order=%d",
		seed, broken[0].n)

	tb.Errorf("%s", out.String())
}

func (o brokenOrder) reportLogs(out *strings.Builder) {
	if len(o.logs) == 0 {
		return
	}
	if o.droppedLogs > 0 {
		fmt.Fprintf(out, "    its log, %d earlier lines dropped:\n", o.droppedLogs)
	} else {
		out.WriteString("    its log:\n")
	}
	for _, line := range o.logs {
		out.WriteString("      ")
		out.WriteString(strings.ReplaceAll(line, "\n", "\n      "))
		out.WriteString("\n")
	}
}

// orderTB collects an order's failures instead of failing the run, so a rush
// reports every broken order rather than only the first one to notice.
type orderTB struct {
	mu          sync.Mutex
	errs        []string
	logs        []string
	droppedLogs int
	cleanups    []func()
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

func (o *orderTB) Logf(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.logs = append(o.logs, fmt.Sprintf(format, args...))
	if len(o.logs) > mostLogLines {
		o.logs = o.logs[1:]
		o.droppedLogs++
	}
}

func (o *orderTB) Failed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.errs) > 0
}

func (o *orderTB) written() (errs, logs []string, droppedLogs int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.errs), slices.Clone(o.logs), o.droppedLogs
}

// finishes runs one part of the order on its own goroutine, where a Fatalf or a
// panic ends only that part and a hang cannot hold up the whole rush.
func (o *orderTB) finishes(part func(), timeout time.Duration, stuck string) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				o.Errorf("kitchen: the order panicked: %v", p)
			}
		}()
		part()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		o.Errorf(stuck, timeout)
		return false
	}
}

// tearDown runs the cleanups last first, the kitchen's own among them, and stops
// at one that hangs.
func (o *orderTB) tearDown(timeout time.Duration) {
	for {
		cleanup, more := o.nextCleanup()
		if !more || !o.finishes(cleanup, timeout, "kitchen: a cleanup was still running after %s") {
			return
		}
	}
}

func (o *orderTB) nextCleanup() (func(), bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.cleanups) == 0 {
		return nil, false
	}
	last := o.cleanups[len(o.cleanups)-1]
	o.cleanups = o.cleanups[:len(o.cleanups)-1]
	return last, true
}
