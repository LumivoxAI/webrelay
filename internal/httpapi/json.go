package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// DecodeJSON decodes exactly one strict JSON object from a request body.
func DecodeJSON(w http.ResponseWriter, r *http.Request, target any, maxBodyBytes int64) *Error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return &Error{Code: CODE_INVALID_REQUEST, Message: "Content-Type must be application/json"}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return decodeError(err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &Error{Code: CODE_INVALID_REQUEST, Message: "request body must contain exactly one JSON object"}
	}
	return nil
}

func decodeError(err error) *Error {
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	var maxBytesError *http.MaxBytesError

	switch {
	case errors.As(err, &syntaxError):
		return &Error{Code: CODE_INVALID_REQUEST, Message: fmt.Sprintf("malformed JSON at character %d", syntaxError.Offset)}
	case errors.Is(err, io.ErrUnexpectedEOF):
		return &Error{Code: CODE_INVALID_REQUEST, Message: "malformed JSON"}
	case errors.As(err, &typeError):
		field := typeError.Field
		if field == "" {
			field = "request body"
		}
		return &Error{Code: CODE_INVALID_REQUEST, Message: fmt.Sprintf("invalid value for %s", field)}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return &Error{Code: CODE_INVALID_REQUEST, Message: "request contains an unknown field"}
	case errors.Is(err, io.EOF):
		return &Error{Code: CODE_INVALID_REQUEST, Message: "request body is required"}
	case errors.As(err, &maxBytesError):
		return &Error{Code: CODE_INVALID_REQUEST, Message: "request body is too large"}
	default:
		return &Error{Code: CODE_INVALID_REQUEST, Message: "invalid JSON request body"}
	}
}
