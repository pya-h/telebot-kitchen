package kitchen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
)

const maxUploadMemory = 8 << 20

type apiMethod func(*Kitchen, params) (any, error)

var apiMethods = map[string]apiMethod{
	"getMe":                  (*Kitchen).getMe,
	"setWebhook":             (*Kitchen).setWebhook,
	"deleteWebhook":          (*Kitchen).deleteWebhook,
	"getWebhookInfo":         (*Kitchen).getWebhookInfo,
	"getChat":                (*Kitchen).getChat,
	"getChatMember":          (*Kitchen).getChatMember,
	"getChatAdministrators":  (*Kitchen).getChatAdministrators,
	"getChatMemberCount":     (*Kitchen).getChatMemberCount,
	"banChatMember":          (*Kitchen).banChatMember,
	"unbanChatMember":        (*Kitchen).unbanChatMember,
	"restrictChatMember":     (*Kitchen).restrictChatMember,
	"promoteChatMember":      (*Kitchen).promoteChatMember,
	"pinChatMessage":         (*Kitchen).pinChatMessage,
	"unpinChatMessage":       (*Kitchen).unpinChatMessage,
	"unpinAllChatMessages":   (*Kitchen).unpinAllChatMessages,
	"sendMessage":            (*Kitchen).sendMessage,
	"sendPhoto":              (*Kitchen).sendPhoto,
	"forwardMessage":         (*Kitchen).forwardMessage,
	"copyMessage":            (*Kitchen).copyMessage,
	"editMessageText":        (*Kitchen).editMessageText,
	"editMessageCaption":     (*Kitchen).editMessageCaption,
	"editMessageReplyMarkup": (*Kitchen).editMessageReplyMarkup,
	"deleteMessage":          (*Kitchen).deleteMessage,
	"answerCallbackQuery":    (*Kitchen).answerCallbackQuery,
}

type params map[string]string

func (p params) decode(name string, dst any) error {
	raw, ok := p[name]
	if !ok {
		return nil
	}
	return json.Unmarshal([]byte(raw), dst)
}

func (p params) chatID() (int64, error) { return p.chat("chat_id") }

func (p params) fromChatID() (int64, error) { return p.chat("from_chat_id") }

// A zero id is an unset one, and Telegram has no chat there either: refusing it
// keeps a bot that forgets to fill the field from passing.
func (p params) chat(name string) (int64, error) {
	id, err := strconv.ParseInt(p[name], 10, 64)
	if err != nil || id == 0 {
		return 0, badRequest(name)
	}
	return id, nil
}

func (p params) messageID() (int, error) {
	id, err := strconv.Atoi(p["message_id"])
	if err != nil || id <= 0 {
		return 0, badRequest("message_id")
	}
	return id, nil
}

// An absent or empty keyboard is normalized to nil, so "no keyboard" has one
// representation whichever way the bot expressed it.
func (p params) markup() (*models.InlineKeyboardMarkup, error) {
	raw, ok := p["reply_markup"]
	if !ok || raw == "" {
		return nil, nil
	}
	markup := &models.InlineKeyboardMarkup{}
	if err := json.Unmarshal([]byte(raw), markup); err != nil {
		return nil, badRequest("reply_markup")
	}
	if len(markup.InlineKeyboard) == 0 {
		return nil, nil
	}
	return markup, nil
}

func (p params) flag(name string) bool { return p[name] == "true" }

func (p params) number(name string) int {
	n, _ := strconv.Atoi(p[name])
	return n
}

func (k *Kitchen) serve(w http.ResponseWriter, r *http.Request) {
	// Even a call the kitchen turns away is progress a waiter may be watching for.
	defer k.activity.note()

	token, method, ok := route(r.URL.Path)
	if !ok {
		writeError(w, &apiError{Code: http.StatusNotFound, Description: "Not Found"})
		return
	}
	if token != k.token {
		writeError(w, &apiError{Code: http.StatusUnauthorized, Description: "Unauthorized"})
		return
	}

	p, err := k.parseParams(r)
	if err != nil {
		writeError(w, &apiError{Code: http.StatusBadRequest, Description: "Bad Request: " + err.Error()})
		return
	}

	handler, ok := apiMethods[method]
	if !ok {
		k.reportUnsupported(method)
		writeError(w, &apiError{Code: http.StatusNotFound, Description: "Not Found: method not found"})
		return
	}

	call := newCall(method, p)
	// Injected before dispatch: a call Telegram refuses never reaches the world.
	if fault, refused := k.faults.pick(call); refused {
		k.calls.record(call.rejected(fault))
		fault.serve(w)
		return
	}
	if err := k.world.moved(p["chat_id"]); err != nil {
		k.calls.record(call.rejected(err))
		writeError(w, err)
		return
	}

	result, err := handler(k, p)
	if err != nil {
		k.calls.record(call.rejected(err))
		writeError(w, err)
		return
	}
	k.calls.record(call)
	writeResult(w, result)
}

// Reported once per method: a bot that polls would otherwise bury the gap under
// thousands of copies of it.
func (k *Kitchen) reportUnsupported(method string) {
	if _, seen := k.unsupported.LoadOrStore(method, struct{}{}); !seen {
		k.tb.Errorf("kitchen: unsupported Bot API method %q", method)
	}
}

func route(path string) (token, method string, ok bool) {
	rest, found := strings.CutPrefix(path, "/bot")
	if !found {
		return "", "", false
	}
	token, method, found = strings.Cut(rest, "/")
	if !found || token == "" || method == "" {
		return "", "", false
	}
	return token, method, true
}

func (k *Kitchen) parseParams(r *http.Request) (params, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.Header.Get("Content-Type") != "" {
		return nil, err
	}

	switch mediaType {
	case "multipart/form-data":
		if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
			if !errors.Is(err, io.EOF) { // a parameterless call sends an empty body
				return nil, err
			}
			return params{}, nil
		}
		p := make(params, len(r.MultipartForm.Value)+len(r.MultipartForm.File))
		for name, values := range r.MultipartForm.Value {
			if len(values) > 0 {
				p[name] = values[0]
			}
		}
		// Stored as a file id, so a method sees one shape for bytes and ids alike.
		for name, headers := range r.MultipartForm.File {
			if len(headers) == 0 {
				continue
			}
			f, err := k.files.upload(headers[0])
			if err != nil {
				return nil, err
			}
			p[name] = f.ID
		}
		return p, nil

	case "application/json":
		var fields map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		p := make(params, len(fields))
		for name, raw := range fields {
			p[name] = unquote(raw)
		}
		return p, nil

	default:
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		p := make(params, len(r.Form))
		for name := range r.Form {
			p[name] = r.Form.Get(name)
		}
		return p, nil
	}
}

func unquote(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

type apiError struct {
	Code            int
	Description     string
	RetryAfter      int
	MigrateToChatID int64
}

func requestError(description string) *apiError {
	return &apiError{Code: http.StatusBadRequest, Description: "Bad Request: " + description}
}

func badRequest(field string) *apiError { return requestError("can't parse field " + field) }

func (e *apiError) Error() string { return fmt.Sprintf("kitchen: %d %s", e.Code, e.Description) }

type response struct {
	OK          bool            `json:"ok"`
	Result      any             `json:"result,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  *responseParams `json:"parameters,omitempty"`
}

type responseParams struct {
	RetryAfter      int   `json:"retry_after,omitempty"`
	MigrateToChatID int64 `json:"migrate_to_chat_id,omitempty"`
}

func writeResult(w http.ResponseWriter, result any) {
	writeResponse(w, http.StatusOK, response{OK: true, Result: result})
}

func writeError(w http.ResponseWriter, err error) {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		apiErr = &apiError{Code: http.StatusInternalServerError, Description: err.Error()}
	}
	res := response{OK: false, ErrorCode: apiErr.Code, Description: apiErr.Description}
	if apiErr.RetryAfter > 0 || apiErr.MigrateToChatID != 0 {
		res.Parameters = &responseParams{RetryAfter: apiErr.RetryAfter, MigrateToChatID: apiErr.MigrateToChatID}
	}
	writeResponse(w, apiErr.Code, res)
}

func writeResponse(w http.ResponseWriter, status int, res response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(res)
}
