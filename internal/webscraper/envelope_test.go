package webscraper

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestEnvelopeMessageFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		message  envelope
		parseErr error
		want     string
	}{
		{"data string", envelope{Data: json.RawMessage(`"API key authentication failed: error-6"`)}, nil, "API key authentication failed: error-6"},
		{"msg priority", envelope{Msg: "msg reason", Message: "message reason", Data: json.RawMessage(`"data reason"`)}, nil, "msg reason"},
		{"message priority", envelope{Message: "message reason", Data: json.RawMessage(`"data reason"`)}, nil, "message reason"},
		{"blank msg", envelope{Msg: " \t", Message: " ", Data: json.RawMessage(`"  data reason  "`)}, nil, "data reason"},
		{"object", envelope{Data: json.RawMessage(`{"private":"not a message"}`)}, nil, "upstream request failed"},
		{"array", envelope{Data: json.RawMessage(`["not a message"]`)}, nil, "upstream request failed"},
		{"null", envelope{Data: json.RawMessage("null")}, nil, "upstream request failed"},
		{"blank data", envelope{Data: json.RawMessage(`"  "`)}, nil, "upstream request failed"},
		{"number", envelope{Data: json.RawMessage("401")}, nil, "upstream request failed"},
		{"parse failure", envelope{}, errors.New("invalid JSON"), "invalid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := envelopeMessage(tc.message, tc.parseErr); got != tc.want {
				t.Fatalf("message = %q, want %q", got, tc.want)
			}
		})
	}
}
