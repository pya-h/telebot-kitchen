package kitchen

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type page struct {
	titles []string
	next   string
}

// paging is a bot that answers each search with the page its offset names, and
// keeps the queries it was asked.
func paging(t *testing.T, k *Kitchen, b *bot.Bot, pages map[string]page) *[]models.InlineQuery {
	t.Helper()
	asked := &[]models.InlineQuery{}
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery == nil {
			return
		}
		*asked = append(*asked, *u.InlineQuery)
		this := pages[u.InlineQuery.Offset]
		results := make([]models.InlineQueryResult, len(this.titles))
		for i, title := range this.titles {
			results[i] = article(title, title, "chose "+title)
		}
		if _, err := b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
			InlineQueryID: u.InlineQuery.ID, Results: results, NextOffset: this.next,
		}); err != nil {
			t.Errorf("answer: %v", err)
		}
	})
	return asked
}

var twoPages = map[string]page{
	"":   {titles: []string{"Ada", "Bob"}, next: "20"},
	"20": {titles: []string{"Cid"}},
}

func TestScrollingResendsTheSearchWithTheOffsetTheBotGave(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	asked := paging(t, k, b, map[string]page{
		"":   {titles: []string{"Ada"}, next: "20"},
		"20": {titles: []string{"Bob"}, next: "40"},
		"40": {titles: []string{"Cid"}},
	})
	ada := k.User(7)

	ada.Search("people")
	k.Settle()
	ada.SearchMore()
	k.Settle()
	ada.SearchMore()
	k.Settle()

	if len(*asked) != 3 {
		t.Fatalf("queries = %+v, want the search and two scrolls", *asked)
	}
	for i, offset := range []string{"", "20", "40"} {
		if got := (*asked)[i]; got.Query != "people" || got.Offset != offset || got.From.ID != 7 {
			t.Errorf("query %d = %+v, want people at offset %q from ada", i, got, offset)
		}
	}
	if (*asked)[0].ID == (*asked)[1].ID {
		t.Errorf("queries share the id %q, want each its own", (*asked)[0].ID)
	}
}

func TestScrollingAsksOnlyWhatAClientWould(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	ada := k.User(7)
	said := func(act func()) string {
		before := len(tb.errors())
		act()
		errs := tb.errors()[before:]
		if len(errs) != 1 {
			t.Fatalf("errors = %q, want exactly one", errs)
		}
		return errs[0]
	}

	if err := said(ada.SearchMore); !strings.Contains(err, "no search to scroll") {
		t.Errorf("before any search: %q", err)
	}

	unanswered := New(tb)
	silent := unanswered.User(7)
	silent.Search("people")
	if err := said(silent.SearchMore); !strings.Contains(err, "not answered") {
		t.Errorf("before the answer: %q", err)
	}

	asked := paging(t, k, b, map[string]page{"": {titles: []string{"Ada"}}})
	ada.Search("people")
	k.Settle()
	if err := said(ada.SearchMore); !strings.Contains(err, "no next_offset") {
		t.Errorf("after an empty next_offset: %q", err)
	}
	if len(*asked) != 1 {
		t.Errorf("queries = %+v, want a refused scroll never to reach the bot", *asked)
	}
}

func TestScrollingBelongsToOneMemberInOneChat(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	asked := paging(t, k, b, twoPages)
	team := k.Group(-42, "Standup")
	ada, bob := k.User(7), k.User(8).In(team)
	adaThere := k.User(7).In(team)

	adaThere.Search("people")
	k.Settle()
	bob.SearchMore()
	ada.SearchMore()

	if errs := tb.errors(); len(errs) != 2 {
		t.Fatalf("errors = %q, want one for each who searched nothing there", errs)
	}
	if len(*asked) != 1 {
		t.Errorf("queries = %+v, want only ada's own search", *asked)
	}
}

func TestAnUnansweredNewSearchIsNotScrolledAsTheOldOne(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	ada := k.User(7)
	answering := true
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery != nil && answering {
			b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
				InlineQueryID: u.InlineQuery.ID, NextOffset: "20",
				Results: []models.InlineQueryResult{article("a", "Ada", "ada")},
			})
		}
	})
	ada.Search("first")
	k.Settle()
	answering = false
	ada.Search("second")
	k.Settle()

	ada.SearchMore()

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], `"second"`) {
		t.Errorf("errors = %q, want the newest search named as the unanswered one", errs)
	}
}

func TestAPickWaitsOnNoSearchTheBotHasNotAnswered(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	answering := true
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery != nil && answering {
			b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
				InlineQueryID: u.InlineQuery.ID, Results: []models.InlineQueryResult{article("a", "Ada", "chose ada")},
			})
		}
	})
	ada := k.User(7)
	ada.Search("first")
	k.Settle()
	answering = false
	ada.Search("second")
	k.Settle()

	ada.Pick("Ada")
	k.Settle()

	if log := ada.History(); len(log) != 1 || log[0].Text != "chose ada" {
		t.Errorf("history = %v, want the pick made from the answered search", log)
	}
}

func TestAResultFromAnEarlierPageCanStillBePicked(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var chosen []*models.ChosenInlineResult
	paging(t, k, b, twoPages)
	answer := k.process
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		answer(ctx, u)
		if u.ChosenInlineResult != nil {
			chosen = append(chosen, u.ChosenInlineResult)
		}
	})
	ada := k.User(7)

	ada.Search("people")
	k.Settle()
	ada.SearchMore()
	k.Settle()
	ada.Pick("Bob")
	k.Settle()
	ada.Pick("Cid")
	k.Settle()

	if len(chosen) != 2 || chosen[0].ResultID != "Bob" || chosen[1].ResultID != "Cid" || chosen[0].Query != "people" {
		t.Fatalf("chosen = %+v, want one from each page, both of the search", chosen)
	}
}

func TestANewSearchDropsThePagesBeforeIt(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	asked := paging(t, k, b, twoPages)
	ada := k.User(7)

	ada.Search("people")
	k.Settle()
	ada.SearchMore()
	k.Settle()
	ada.Search("others")
	k.Settle()
	ada.Pick("Cid")

	if got := (*asked)[2]; got.Query != "others" || got.Offset != "" {
		t.Errorf("query = %+v, want a new search from the top", got)
	}
	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], `no "Cid"`) {
		t.Errorf("errors = %q, want the last search's second page gone", errs)
	}
}

func TestNextOffsetHoldsSixtyFourBytes(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var id string
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.InlineQuery != nil {
			id = u.InlineQuery.ID
		}
	})
	ada := k.User(7)
	ada.Search("people")
	k.Settle()

	send := func(offset string) error {
		_, err := b.AnswerInlineQuery(context.Background(), &bot.AnswerInlineQueryParams{
			InlineQueryID: id, Results: []models.InlineQueryResult{article("a", "Ada", "ada")}, NextOffset: offset,
		})
		return err
	}

	if err := send(strings.Repeat("é", 32) + "x"); err == nil || !strings.Contains(err.Error(), "NEXT_OFFSET_INVALID") {
		t.Errorf("65 bytes: err = %v, want it refused", err)
	}
	if err := send(strings.Repeat("é", 32)); err != nil {
		t.Errorf("64 bytes, after a refusal that uses nothing up: %v", err)
	}
}

func TestAQueryReportsTheChatTypeTelegramDoes(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	asked := paging(t, k, b, map[string]page{"": {titles: []string{"Ada"}, next: "1"}, "1": {titles: []string{"Bob"}}})

	for _, c := range []struct {
		want string
		from *Member
	}{
		{"sender", k.User(7).Member},
		{"group", k.User(8).In(k.Group(-42, "Standup"))},
		{"supergroup", k.User(9).In(k.Supergroup(-43, "Town"))},
		{"channel", k.User(10).In(k.Channel(-44, "News"))},
	} {
		before := len(*asked)
		c.from.Search("x")
		k.Settle()
		c.from.SearchMore()
		k.Settle()
		for _, got := range (*asked)[before:] {
			if got.ChatType != c.want {
				t.Errorf("chat_type = %q, want %q", got.ChatType, c.want)
			}
		}
		if len(*asked) != before+2 {
			t.Errorf("queries = %d more, want the search and its scroll", len(*asked)-before)
		}
	}
}

// drawer has one button of each kind that opens an inline query.
func drawer() *models.InlineKeyboardMarkup {
	here, there, empty := "people", "share me", ""
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "Full list", SwitchInlineQueryCurrentChat: &here}},
		{{Text: "Bare", SwitchInlineQueryCurrentChat: &empty}},
		{{Text: "Share", SwitchInlineQuery: &there}},
		{{Text: "Share to a group", SwitchInlineQueryChosenChat: &models.SwitchInlineQueryChosenChat{Query: "to you", AllowGroupChats: true}}},
		{{Text: "Back", CallbackData: "back"}},
	}}
}

func showDrawer(t *testing.T, b *bot.Bot, chatID int64) {
	t.Helper()
	if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID: chatID, Text: "A list", ReplyMarkup: drawer(),
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestAButtonShowsTheQueryItWouldOpen(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7, Started())
	showDrawer(t, b, ada.ID())

	shown := ada.Screen()
	full, _ := shown.Button("Full list")
	if full.SwitchInlineQueryCurrentChat == nil || *full.SwitchInlineQueryCurrentChat != "people" {
		t.Errorf("Full list = %+v, want its query", full)
	}
	if full.SwitchInlineQuery != nil || full.SwitchInlineQueryChosenChat != nil {
		t.Errorf("Full list = %+v, want it to be of one kind", full)
	}
	bare, _ := shown.Button("Bare")
	if bare.SwitchInlineQueryCurrentChat == nil || *bare.SwitchInlineQueryCurrentChat != "" {
		t.Errorf("Bare = %+v, want an empty query told apart from none", bare)
	}
	share, _ := shown.Button("Share")
	if share.SwitchInlineQuery == nil || *share.SwitchInlineQuery != "share me" {
		t.Errorf("Share = %+v, want its query", share)
	}
	chosen, _ := shown.Button("Share to a group")
	if want := (ChosenChat{Query: "to you", AllowGroupChats: true}); chosen.SwitchInlineQueryChosenChat == nil || *chosen.SwitchInlineQueryChosenChat != want {
		t.Errorf("Share to a group = %+v, want %+v", chosen, want)
	}
	back, _ := shown.Button("Back")
	if back.switchesInline() {
		t.Errorf("Back = %+v, want an ordinary button to carry none of them", back)
	}

	call, _ := k.Calls().First(Method("sendMessage"))
	if got := call.Keyboard()[0][0].SwitchInlineQueryCurrentChat; got == nil || *got != "people" {
		t.Errorf("the recorded call shows %v, want the same query", got)
	}
}

func TestTappingASwitchButtonOpensTheSearchInThatChat(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	asked := paging(t, k, b, twoPages)
	team := k.Group(-42, "Standup")
	ada := k.User(7).In(team)
	ada.Join()
	k.Settle()
	showDrawer(t, b, team.ID())
	before := len(team.History())

	ada.TapSwitch("Full list")
	k.Settle()
	ada.SearchMore()
	k.Settle()
	ada.TapSwitch("Bare")
	k.Settle()

	if len(*asked) != 3 {
		t.Fatalf("queries = %+v, want the tap, its scroll and the bare tap", *asked)
	}
	if got := (*asked)[0]; got.Query != "people" || got.Offset != "" || got.From.ID != 7 || got.ChatType != "group" {
		t.Errorf("tap = %+v, want ada's search for the button's text in the group", got)
	}
	if got := (*asked)[1]; got.Query != "people" || got.Offset != "20" {
		t.Errorf("scroll = %+v, want a tapped search to scroll like a typed one", got)
	}
	if got := (*asked)[2]; got.Query != "" {
		t.Errorf("bare tap = %+v, want only the bot's name typed", got)
	}
	if log := team.History(); len(log) != before {
		t.Errorf("history = %v, want a tap to put nothing in the chat", log)
	}
}

func TestAnAnswerToATappedSwitchIsStillChecked(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var err error
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery != nil {
			_, err = b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
				InlineQueryID: u.InlineQuery.ID,
				Results:       []models.InlineQueryResult{article("a", "One", "1"), article("a", "Two", "2")},
			})
		}
	})
	ada := k.User(7, Started())
	showDrawer(t, b, ada.ID())

	ada.TapSwitch("Full list")
	k.Settle()

	if err == nil || !strings.Contains(err.Error(), "RESULT_ID_DUPLICATE") {
		t.Errorf("err = %v, want two results sharing an id refused", err)
	}
}

func TestTheWrongPressOfASwitchButtonIsATestError(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	var got updates
	got.collect(k)
	ada := k.User(7, Started())
	news := k.Channel(-44, "News")
	reader := k.User(8).In(news)
	reader.Join()
	k.Settle()
	showDrawer(t, b, ada.ID())
	showDrawer(t, b, news.ID())
	seen := len(got.all())

	for _, c := range []struct {
		name string
		act  func()
		want string
	}{
		{"tap as a callback", func() { ada.Tap("Full list") }, "takes TapSwitch"},
		{"tap the sharing kind", func() { ada.Tap("Share") }, "takes TapSwitch"},
		{"switch a callback button", func() { ada.TapSwitch("Back") }, "opens no inline query"},
		{"switch the sharing kind", func() { ada.TapSwitch("Share") }, "choose a chat first"},
		{"switch the chosen kind", func() { ada.TapSwitch("Share to a group") }, "choose a chat first"},
		{"switch a missing button", func() { ada.TapSwitch("Nope") }, `no button "Nope"`},
		{"switch in a channel", func() { reader.TapSwitch("Full list") }, "in a channel"},
	} {
		before := len(tb.errors())
		c.act()
		if errs := tb.errors()[before:]; len(errs) != 1 || !strings.Contains(errs[0], c.want) {
			t.Errorf("%s: errors = %q, want one saying %q", c.name, errs, c.want)
		}
	}
	if now := got.all(); len(now) != seen {
		t.Errorf("updates = %+v, want a wrong press to reach nothing", now[seen:])
	}
}

func TestAPagedSearchOfTwentySurvivesTheWholeScroll(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	pages := map[string]page{}
	const size, total = 20, 65
	for from := 0; from < total; from += size {
		var this page
		for i := from; i < min(from+size, total); i++ {
			this.titles = append(this.titles, "item "+strconv.Itoa(i))
		}
		if from+size < total {
			this.next = strconv.Itoa(from + size)
		}
		offset := ""
		if from > 0 {
			offset = strconv.Itoa(from)
		}
		pages[offset] = this
	}
	asked := paging(t, k, b, pages)
	ada := k.User(7)

	ada.Search("all")
	k.Settle()
	for range 3 {
		ada.SearchMore()
		k.Settle()
	}
	ada.Pick("item 3")
	k.Settle()
	ada.Pick("item 64")
	k.Settle()

	if len(*asked) != 4 {
		t.Errorf("queries = %d, want four pages", len(*asked))
	}
	if log := ada.History(); len(log) != 2 || log[1].Text != "chose item 64" {
		t.Errorf("history = %v, want a pick from the first and the last page", log)
	}
}
