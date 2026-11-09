package api

import (
	"encoding/json"
	"net/http"
)

// Refusal codes. The code is the contract; the message is not, and nothing may match on it.
const (
	CodeInvalid              = "invalid"
	CodeUnauthenticated      = "unauthenticated"
	CodeForbidden            = "forbidden"
	CodeNotFound             = "not_found"
	CodeConflict             = "conflict"
	CodeUnsupportedMediaType = "unsupported_media_type"
	CodeBodyTooLarge         = "body_too_large"
	CodeRateLimited          = "rate_limited"
	CodeInternal             = "internal"
)

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// refuse is the only place a refusal is written, so there is one spelling of the envelope.
func refuse(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Code: code, Message: message})
}
