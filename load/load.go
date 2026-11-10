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

type Run struct {
	Orders      int                          // conversations to run
	Concurrency int                          // how many at once; sockets, not memory, are the ceiling
	Seed        int64                        // what Order.Rand runs from
	Timeout     time.Duration                // how long one order may take before it counts as stuck
	Options     []kitchen.Option             // what every kitchen in the run is built with
	Bot         func(*kitchen.Kitchen) error // build the bot and bind it, once per kitchen
	Build       func(*Order) error           // instead of Bot, timed apart from the conversation
	Serve       func(*Order)                 // the conversation to measure
}

// Order is one conversation, with the whole kitchen surface on it.
type Order struct {
	*kitchen.Kitchen

	Ticket string
	N      int
	Rand   *rand.Rand

	steps *stepLog
	tb    *orderTB
}

func (o *Order) Helper() {}

func (o *Order) Errorf(format string, args ...any) { o.tb.Errorf(format, args...) }

// Fatalf ends the goroutine it is called on, so call it from the script itself.
func (o *Order) Fatalf(format string, args ...any) {
	o.tb.Errorf(format, args...)
	runtime.Goexit()
}

func (o *Order) Logf(string, ...any) {}

func (o *Order) Failed() bool { return o.tb.failed() }

// Step times one part of the conversation, so the report can say where the time
// went. Name it after the verb it wraps, or after what it achieves.
func (o *Order) Step(name string, do func()) {
	started := time.Now()
	do()
	o.steps.add(name, time.Since(started))
}

func (r Run) Measure() Report {
	orders, concurrency, seed := r.Orders, r.Concurrency, r.Seed
	if orders <= 0 {
		orders = defaultOrders
	}
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	if seed == 0 {
		seed = defaultSeed
	}

	report := Report{Orders: orders, Concurrency: concurrency, Seed: seed}
	if r.Serve == nil || (r.Bot == nil) == (r.Build == nil) {
		report.Failures = map[string]int{"setup": orders}
		report.Samples = map[string]string{"setup": "a run needs exactly one of Bot and Build, and a Serve"}
		return report
	}

	// Measured first, so the bot's numbers are read against a kitchen that has
	// not yet warmed anything up.
	report.Baseline = r.baseline(orders, concurrency)

	peak := watchGoroutines()
	steps := newStepLog()
	conversations := make([]time.Duration, orders)
	var builds []time.Duration
	broken := newBreakage()
	var mu sync.Mutex

	started := time.Now()
	run(orders, concurrency, func(n int) {
		result := r.serveOne(n, seed, steps)
		conversations[n-1] = result.took
		broken.add(n, result.failed, result.why)
		if r.Build != nil {
			mu.Lock()
			defer mu.Unlock()
			builds = append(builds, result.build)
		}
	})
	report.Elapsed = time.Since(started)
	report.Goroutines = peak()

	report.Rate = float64(orders) / report.Elapsed.Seconds()
	report.Order = spread("conversation", conversations)
	report.Build = spread("build", builds)
	report.Steps = steps.spreads()
	report.Failures, report.Samples = broken.counts, broken.samples
	return report
}

type orderResult struct {
	took, build time.Duration
	failed, why string
}

func (r Run) serveOne(n int, seed int64, steps *stepLog) orderResult {
	tb := &orderTB{}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultOrderTime
	}
	started := time.Now()

	order := &Order{
		Kitchen: kitchen.New(tb, append(slices.Clone(r.Options), kitchen.WithChargeIDs(kitchen.OrderChargeIDs(n)))...),
		Ticket:  "ticket-" + strconv.Itoa(n),
		N:       n,
		Rand:    rand.New(rand.NewPCG(uint64(seed), uint64(n))),
		steps:   steps,
		tb:      tb,
	}

	building := time.Now()
	if !tb.finishes(func() { r.build(order, seed) }, timeout, "the bot was still being built after %s") {
		return orderResult{took: time.Since(started), failed: "stuck", why: tb.lastSaid()}
	}
	// Only Build is apart: with Bot the conversation goes on counting what it always has.
	var built time.Duration
	if r.Build != nil {
		built = time.Since(building)
	}
	if tb.failed() {
		tb.tearDown(timeout)
		return orderResult{took: time.Since(started) - built, build: built, failed: "build", why: tb.firstSaid()}
	}

	if !tb.finishes(func() { r.Serve(order) }, timeout, "the order was still working after %s") {
		return orderResult{took: time.Since(started) - built, build: built, failed: "stuck", why: tb.lastSaid()}
	}
	result := orderResult{took: time.Since(started) - built, build: built}
	switch {
	case !tb.tearDown(timeout):
		result.failed, result.why = "stuck", tb.lastSaid()
	case tb.failed():
		result.failed, result.why = "assertion", tb.firstSaid()
	}
	return result
}

func (r Run) build(o *Order, seed int64) {
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
		o.tb.Errorf("build the bot: %v", err)
	}
}

// breakage counts failures by kind, keeping the lowest order's message for each,
// so two runs of the same script show the same sample.
type breakage struct {
	mu      sync.Mutex
	counts  map[string]int
	samples map[string]string
	from    map[string]int
}

func newBreakage() *breakage {
	return &breakage{counts: map[string]int{}, samples: map[string]string{}, from: map[string]int{}}
}

func (b *breakage) add(n int, kind, why string) {
	if kind == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	b.counts[kind]++
	if first, seen := b.from[kind]; !seen || n < first {
		b.from[kind] = n
		b.samples[kind] = fmt.Sprintf("order %d: %s", n, why)
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
		tb.tearDown(defaultOrderTime)

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
	said     []string
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
	o.said = append(o.said, fmt.Sprintf(format, args...))
}

func (o *orderTB) Logf(string, ...any) {}

func (o *orderTB) Failed() bool { return o.failed() }

func (o *orderTB) failed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.said) > 0
}

func (o *orderTB) firstSaid() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.said[0]
}

func (o *orderTB) lastSaid() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.said[len(o.said)-1]
}

// finishes runs one part of the order on its own goroutine, where a Fatalf or a
// panic ends only that part and a hang cannot hold up the whole run.
func (o *orderTB) finishes(part func(), timeout time.Duration, stuck string) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				o.Errorf("the order panicked: %v", p)
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

// tearDown runs the cleanups last first, the kitchen's own among them, and says
// false at one that hangs.
func (o *orderTB) tearDown(timeout time.Duration) bool {
	for {
		cleanup, more := o.nextCleanup()
		if !more {
			return true
		}
		if !o.finishes(cleanup, timeout, "a cleanup was still running after %s") {
			return false
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

// Report is what a run measured. Every duration is real time.
type Report struct {
	Orders      int
	Concurrency int
	Elapsed     time.Duration
	Rate        float64 // orders per second

	Seed int64 // what Order.Rand ran from

	Order    Spread // the whole conversation, with a Build's time taken out
	Build    Spread // what Build took, when the run has one
	Steps    []Spread
	Baseline Spread // the same lifecycle with no bot in it

	Failures map[string]int
	Samples  map[string]string // the lowest order's message for each kind of failure

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
	fmt.Fprintf(&out, "%d orders at %d-way concurrency in %s — %.0f orders/sec, seed %d\n\n",
		r.Orders, r.Concurrency, round(r.Elapsed), r.Rate, r.Seed)

	fmt.Fprintf(&out, "  %-22s %s\n", r.Order.Name, r.Order.line())
	if r.Build.Count > 0 {
		fmt.Fprintf(&out, "  %-22s %s\n", r.Build.Name, r.Build.line())
	}
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
		if sample := r.Samples[kind]; sample != "" {
			fmt.Fprintf(&out, "    %s\n", strings.ReplaceAll(sample, "\n", "\n    "))
		}
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
