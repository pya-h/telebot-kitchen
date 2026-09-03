package load

import (
	"context"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
	kitchen "github.com/pya-h/telebot-kitchen"
)

const (
	defaultOrders      = 100
	defaultConcurrency = 8
	defaultOrderTime   = 10 * time.Second
	defaultSeed        = 1

	sampleEvery = 10 * time.Millisecond
)

// Run is one measured pass: the same conversation, many times over, with a
// kitchen per order the way a rush does it.
type Run struct {
	Orders      int                          // conversations to run
	Concurrency int                          // how many at once; sockets, not memory, are the ceiling
	Seed        int64                        // what Order.Rand runs from
	Timeout     time.Duration                // how long one order may take before it counts as stuck
	Options     []kitchen.Option             // what every kitchen in the run is built with
	Bot         func(*kitchen.Kitchen) error // build the bot and bind it, once per kitchen
	Serve       func(*Order)                 // the conversation to measure
}

// Order is one conversation, with the whole kitchen surface on it.
type Order struct {
	*kitchen.Kitchen

	Ticket string
	N      int
	Rand   *rand.Rand

	steps *stepLog
}

// Step times one part of the conversation, so the report can say where the time
// went. Name it after the verb it wraps, or after what it achieves.
func (o *Order) Step(name string, do func()) {
	started := time.Now()
	do()
	o.steps.add(name, time.Since(started))
}

func (r Run) Measure() Report {
	orders, concurrency := r.Orders, r.Concurrency
	if orders <= 0 {
		orders = defaultOrders
	}
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	report := Report{Orders: orders, Concurrency: concurrency}
	if r.Bot == nil || r.Serve == nil {
		report.Failures = map[string]int{"setup": orders}
		return report
	}

	// Measured first, so the bot's numbers are read against a kitchen that has
	// not yet warmed anything up.
	report.Baseline = r.baseline(orders, concurrency)

	peak := watchGoroutines()
	steps := newStepLog()
	conversations := make([]time.Duration, orders)
	failures := map[string]int{}
	var mu sync.Mutex

	started := time.Now()
	run(orders, concurrency, func(n int) {
		took, kind := r.serveOne(n, steps)
		conversations[n-1] = took
		if kind != "" {
			mu.Lock()
			defer mu.Unlock()
			failures[kind]++
		}
	})
	report.Elapsed = time.Since(started)
	report.Goroutines = peak()

	report.Rate = float64(orders) / report.Elapsed.Seconds()
	report.Order = spread("conversation", conversations)
	report.Steps = steps.spreads()
	report.Failures = failures
	return report
}

func (r Run) serveOne(n int, steps *stepLog) (took time.Duration, failed string) {
	tb := &orderTB{}
	started := time.Now()

	k := kitchen.New(tb, r.Options...)
	if err := r.Bot(k); err != nil {
		tb.close()
		return time.Since(started), "build"
	}

	seed := r.Seed
	if seed == 0 {
		seed = defaultSeed
	}
	order := &Order{
		Kitchen: k,
		Ticket:  "ticket-" + strconv.Itoa(n),
		N:       n,
		Rand:    rand.New(rand.NewPCG(uint64(seed), uint64(n))),
		steps:   steps,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				tb.Errorf("the order panicked: %v", p)
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
		took = time.Since(started)
		tb.close()
		if tb.failed() {
			return took, "assertion"
		}
		return took, ""
	case <-stuck.C:
		// Its kitchen is left standing: tearing it down would block on whatever
		// the script is stuck in.
		return time.Since(started), "stuck"
	}
}

// baseline is what a conversation costs before the bot does anything: a kitchen
// up, one update through a handler that ignores it, and the kitchen down again.
// Without it every number silently carries the harness's own cost.
func (r Run) baseline(orders, concurrency int) Spread {
	empty := make([]time.Duration, orders)
	run(orders, concurrency, func(n int) {
		started := time.Now()

		tb := &orderTB{}
		k := kitchen.New(tb, r.Options...)
		k.DeliverTo(func(context.Context, *models.Update) {})
		k.User(int64(n)).Send("ping")
		tb.close()

		empty[n-1] = time.Since(started)
	})
	return spread("kitchen alone", empty)
}

func run(orders, concurrency int, one func(n int)) {
	slots := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for n := 1; n <= orders; n++ {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			one(n)
		}()
	}
	wg.Wait()
}

// watchGoroutines samples until the returned function asks for the peak.
func watchGoroutines() func() int {
	peak := runtime.NumGoroutine()
	done := make(chan struct{})
	read := make(chan int)

	go func() {
		tick := time.NewTicker(sampleEvery)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				peak = max(peak, runtime.NumGoroutine())
			case <-done:
				read <- peak
				return
			}
		}
	}()

	return func() int {
		close(done)
		return <-read
	}
}

type stepLog struct {
	mu    sync.Mutex
	times map[string][]time.Duration
}

func newStepLog() *stepLog { return &stepLog{times: map[string][]time.Duration{}} }

func (l *stepLog) add(name string, took time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.times[name] = append(l.times[name], took)
}

// Sorted by name, so two runs of the same script report in the same order.
func (l *stepLog) spreads() []Spread {
	l.mu.Lock()
	defer l.mu.Unlock()

	names := slices.Sorted(maps.Keys(l.times))
	spreads := make([]Spread, len(names))
	for i, name := range names {
		spreads[i] = spread(name, l.times[name])
	}
	return spreads
}

// orderTB catches an order's failures: a load run counts them by kind rather
// than reporting each one, which is what a rush is for.
type orderTB struct {
	mu       sync.Mutex
	broke    bool
	cleanups []func()
}

func (o *orderTB) Cleanup(f func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cleanups = append(o.cleanups, f)
}

func (o *orderTB) Errorf(string, ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.broke = true
}

func (o *orderTB) Failed() bool { return o.failed() }

func (o *orderTB) failed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.broke
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

// Report is what a run measured. Every duration is real time.
type Report struct {
	Orders      int
	Concurrency int
	Elapsed     time.Duration
	Rate        float64 // orders per second

	Order    Spread // the whole conversation
	Steps    []Spread
	Baseline Spread // the same lifecycle with no bot in it

	// Failures counts the orders that broke, by kind: "assertion" for a script
	// that reported, "stuck" for one that outlived its timeout, "build" for a
	// bot that would not construct.
	Failures map[string]int

	Goroutines int // the peak seen while the run was going
}

// Net is the conversation's typical cost with the kitchen's own subtracted, so
// the number belongs to the bot rather than to the harness.
func (r Report) Net() time.Duration { return r.Order.P50 - r.Baseline.P50 }

type Spread struct {
	Name          string
	Count         int
	P50, P95, Max time.Duration
}

func spread(name string, took []time.Duration) Spread {
	if len(took) == 0 {
		return Spread{Name: name}
	}
	sorted := slices.Clone(took)
	slices.Sort(sorted)
	return Spread{
		Name:  name,
		Count: len(sorted),
		P50:   percentile(sorted, 0.50),
		P95:   percentile(sorted, 0.95),
		Max:   sorted[len(sorted)-1],
	}
}

// Nearest rank: at these sample counts nothing subtler earns its keep.
func percentile(sorted []time.Duration, q float64) time.Duration {
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(i, 0)]
}

func (r Report) String() string {
	var out strings.Builder
	fmt.Fprintf(&out, "%d orders at %d-way concurrency in %s — %.0f orders/sec\n\n",
		r.Orders, r.Concurrency, round(r.Elapsed), r.Rate)

	fmt.Fprintf(&out, "  %-22s %s\n", r.Order.Name, r.Order.line())
	fmt.Fprintf(&out, "  %-22s %s\n", r.Baseline.Name, r.Baseline.line())
	fmt.Fprintf(&out, "  %-22s p50 %s\n\n", "net of the kitchen", round(r.Net()))

	if len(r.Steps) > 0 {
		out.WriteString("  step\n")
		for _, s := range r.Steps {
			fmt.Fprintf(&out, "  %-22s %s\n", s.Name, s.line())
		}
		out.WriteString("\n")
	}

	fmt.Fprintf(&out, "  peak goroutines %d\n", r.Goroutines)
	if len(r.Failures) == 0 {
		out.WriteString("  every order came back clean\n")
		return out.String()
	}
	for _, kind := range slices.Sorted(maps.Keys(r.Failures)) {
		fmt.Fprintf(&out, "  %s: %d orders\n", kind, r.Failures[kind])
	}
	return out.String()
}

func (s Spread) line() string {
	return fmt.Sprintf("n %-5d p50 %-9s p95 %-9s max %s",
		s.Count, round(s.P50), round(s.P95), round(s.Max))
}

// Sub-microsecond digits are noise at this resolution.
func round(d time.Duration) string {
	if d >= time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Microsecond).String()
}
