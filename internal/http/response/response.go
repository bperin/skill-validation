// Package response writes deliberate public HTTP responses.
package response

import (
	"encoding/json"
	"net/http"
)

type ErrorPayload struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

func Error(w http.ResponseWriter, status int, code, description string) {
	JSON(w, status, ErrorPayload{Error: code, ErrorDescription: description})
}
