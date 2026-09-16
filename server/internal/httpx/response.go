package httpx

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// envelope is the single response shape for errors so clients parse one thing.
type envelope struct {
	Error *Error `json:"error"`
}

// JSON writes v with the given status. It buffers first so a mid-encode failure
// cannot emit a half-written body under an already-committed 200.
func JSON(w http.ResponseWriter, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		slog.Error("httpx: marshal response", slog.Any("error", err))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"Внутренняя ошибка сервера"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

// JSONWithETag writes v and short-circuits to 304 when the client's
// If-None-Match already matches. Storefront responses are read-mostly and
// identical for every anonymous client, so this removes most body bytes.
func JSONWithETag(w http.ResponseWriter, r *http.Request, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		Respond(w, r, Internal("Внутренняя ошибка сервера").WithCause(err))
		return
	}
	sum := sha256.Sum256(buf)
	etag := `"` + base64.RawURLEncoding.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)

	if match := r.Header.Get("If-None-Match"); match != "" {
		for _, candidate := range strings.Split(match, ",") {
			if strings.TrimSpace(candidate) == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

// NoContent ends the request with 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Respond renders err, logging the internal cause but never leaking it.
func Respond(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := AsError(err)
	if apiErr.Status() >= 500 {
		slog.ErrorContext(r.Context(), "request failed",
			slog.String("path", r.URL.Path),
			slog.String("method", r.Method),
			slog.String("code", string(apiErr.Code)),
			slog.Any("error", apiErr.Error()))
	}
	JSON(w, apiErr.Status(), envelope{Error: &Error{
		Code:    apiErr.Code,
		Message: apiErr.Message,
		Fields:  apiErr.Fields,
	}})
}

// DecodeJSON reads a JSON body with a hard size cap and strict field checking,
// translating the many ways decoding fails into one clean 400.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mt := strings.TrimSpace(strings.Split(ct, ";")[0]); mt != "application/json" {
			return BadRequest("Ожидается Content-Type: application/json")
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &syntaxErr):
			return BadRequest(fmt.Sprintf("Некорректный JSON (позиция %d)", syntaxErr.Offset))
		case errors.As(err, &typeErr):
			return BadRequest(fmt.Sprintf("Поле %q имеет неверный тип", typeErr.Field))
		case errors.As(err, &maxErr):
			return newError(http.StatusRequestEntityTooLarge, CodePayloadTooBig, "Тело запроса слишком большое")
		case errors.Is(err, io.EOF):
			return BadRequest("Пустое тело запроса")
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			field := strings.TrimPrefix(err.Error(), "json: unknown field ")
			return BadRequest(fmt.Sprintf("Неизвестное поле %s", field))
		default:
			return BadRequest("Не удалось разобрать тело запроса")
		}
	}
	// A second value means the client sent concatenated documents.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("Тело запроса должно содержать один JSON-объект")
	}
	return nil
}

// LogWarn records a non-fatal failure: work that did not succeed but must not
// fail the request, such as a metric write.
func LogWarn(r *http.Request, msg string, err error) {
	slog.WarnContext(r.Context(), msg,
		slog.String("path", r.URL.Path),
		slog.Any("error", err))
}
