package kitchen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

const maxUploadMemory = 8 << 20

type apiMethod func(*Kitchen, params) (any, error)

var apiMethods = map[string]apiMethod{
	"getMe":          (*Kitchen).getMe,
	"setWebhook":     (*Kitchen).setWebhook,
	"deleteWebhook":  (*Kitchen).deleteWebhook,
	"getWebhookInfo": (*Kitchen).getWebhookInfo,
}

type params map[string]string

func (p params) decode(name string, dst any) error {
	raw, ok := p[name]
	if !ok {
		return nil
	}
	return json.Unmarshal([]byte(raw), dst)
}

func (k *Kitchen) serve(w http.ResponseWriter, r *http.Request) {
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
		k.tb.Errorf("kitchen: unsupported Bot API method %q", method)
		writeError(w, &apiError{Code: http.StatusNotFound, Description: "Not Found: method not found"})
		return
	}

	result, err := handler(k, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, result)
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
		// An upload is stored and then read as the file id it was issued, so a
		// method sees one shape whether the bot sent bytes or an existing id.
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
	Code        int
	Description string
	RetryAfter  int
}

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
	if apiErr.RetryAfter > 0 {
		res.Parameters = &responseParams{RetryAfter: apiErr.RetryAfter}
	}
	writeResponse(w, apiErr.Code, res)
}

func writeResponse(w http.ResponseWriter, status int, res response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(res)
}
