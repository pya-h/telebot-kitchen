package kitchen

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
)

const controlPrefix = "/kitchen/"

type order struct {
	ID       int64    `json:"id"`
	Type     string   `json:"type"`
	Title    string   `json:"title"`
	First    string   `json:"first_name"`
	Last     string   `json:"last_name"`
	Username string   `json:"username"`
	Language string   `json:"language"`
	User     int64    `json:"user"`
	Chat     int64    `json:"chat"`
	Text     string   `json:"text"`
	Name     string   `json:"name"`
	Args     []string `json:"args"`
	Button   string   `json:"button"`
	Started  bool     `json:"started"`
}

var controlVerbs = map[string]func(*Kitchen, order) any{
	"chat":       (*Kitchen).describeChat,
	"user":       (*Kitchen).introduce,
	"join":       (*Kitchen).controlJoin,
	"leave":      (*Kitchen).controlLeave,
	"send":       (*Kitchen).controlSend,
	"command":    (*Kitchen).controlCommand,
	"tap":        (*Kitchen).controlTap,
	"settle":     (*Kitchen).controlSettle,
	"screen":     (*Kitchen).controlScreen,
	"history":    (*Kitchen).controlHistory,
	"transcript": (*Kitchen).controlTranscript,
	"calls":      (*Kitchen).controlCalls,
}

func (k *Kitchen) control(w http.ResponseWriter, r *http.Request) {
	verb, known := controlVerbs[strings.TrimPrefix(r.URL.Path, controlPrefix)]
	if !known {
		http.NotFound(w, r)
		return
	}

	asked, err := ordered(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// The kitchen answers a test by failing it. There is no test here, so what
	// it would have said comes back with the reply instead.
	said, watching := k.tb.(*Log)
	mark := 0
	if watching {
		mark = said.count()
	}
	result := verb(k, asked)

	answer := map[string]any{"ok": true}
	if result != nil {
		answer["result"] = result
	}
	// Ahead of the status: a header set after one never goes out.
	w.Header().Set("Content-Type", "application/json")
	if watching {
		if wrong := said.since(mark); len(wrong) > 0 {
			answer["ok"], answer["errors"] = false, wrong
			w.WriteHeader(http.StatusBadRequest)
		}
	}
	json.NewEncoder(w).Encode(answer)
}

func ordered(r *http.Request) (order, error) {
	var asked order
	if r.Method != http.MethodGet {
		if err := json.NewDecoder(r.Body).Decode(&asked); err != nil && !errors.Is(err, io.EOF) {
			return asked, err
		}
		return asked, nil
	}
	q := r.URL.Query()
	asked.ID, _ = strconv.ParseInt(q.Get("id"), 10, 64)
	asked.User, _ = strconv.ParseInt(q.Get("user"), 10, 64)
	asked.Chat, _ = strconv.ParseInt(q.Get("chat"), 10, 64)
	asked.Type, asked.Title = q.Get("type"), q.Get("title")
	asked.Text, asked.Name, asked.Button = q.Get("text"), q.Get("name"), q.Get("button")
	asked.First, asked.Last, asked.Username = q.Get("first_name"), q.Get("last_name"), q.Get("username")
	asked.Language, asked.Args = q.Get("language"), q["arg"]
	asked.Started = q.Get("started") == "true"
	return asked, nil
}

func (k *Kitchen) describeChat(asked order) any {
	switch models.ChatType(asked.Type) {
	case models.ChatTypeGroup:
		k.Group(asked.ID, asked.Title)
	case models.ChatTypeSupergroup:
		k.Supergroup(asked.ID, asked.Title)
	case models.ChatTypeChannel:
		k.Channel(asked.ID, asked.Title)
	default:
		k.tb.Errorf("kitchen: no chat is a %q; say group, supergroup or channel", asked.Type)
	}
	return nil
}

func (k *Kitchen) introduce(asked order) any {
	var named []UserOption
	if asked.First != "" {
		named = append(named, WithFullName(asked.First, asked.Last))
	}
	if asked.Username != "" {
		named = append(named, WithUsername(asked.Username))
	}
	if asked.Language != "" {
		named = append(named, WithLanguage(asked.Language))
	}
	if asked.Started {
		named = append(named, Started())
	}
	k.User(asked.ID, named...)
	return nil
}

func (k *Kitchen) acting(asked order) *Member {
	person := k.User(asked.User)
	if asked.Chat == 0 || asked.Chat == asked.User {
		return person.Member
	}
	return person.In(k.knownChat(asked.Chat))
}

func (k *Kitchen) knownChat(id int64) *Chat {
	info, known := k.world.info(id)
	if !known {
		k.tb.Errorf("kitchen: chat %d is not one the kitchen has been told about", id)
		return &Chat{kitchen: k, id: id, kind: models.ChatTypeSupergroup}
	}
	return &Chat{kitchen: k, id: id, kind: info.Type}
}

func (k *Kitchen) controlJoin(asked order) any  { k.acting(asked).Join(); return nil }
func (k *Kitchen) controlLeave(asked order) any { k.acting(asked).Leave(); return nil }

func (k *Kitchen) controlSend(asked order) any {
	k.acting(asked).Send(asked.Text)
	return nil
}

func (k *Kitchen) controlCommand(asked order) any {
	k.acting(asked).SendCommand(asked.Name, asked.Args...)
	return nil
}

func (k *Kitchen) controlTap(asked order) any {
	k.acting(asked).Tap(asked.Button)
	return nil
}

func (k *Kitchen) controlSettle(order) any { k.Settle(); return nil }

func (k *Kitchen) controlScreen(asked order) any { return k.acting(asked).Screen() }

func (k *Kitchen) controlHistory(asked order) any { return k.History(k.spoken(asked)) }

func (k *Kitchen) controlTranscript(asked order) any { return k.Transcript(k.spoken(asked)) }

func (k *Kitchen) controlCalls(order) any { return k.Calls() }

func (k *Kitchen) spoken(asked order) int64 {
	if asked.Chat != 0 {
		return asked.Chat
	}
	return asked.User
}
