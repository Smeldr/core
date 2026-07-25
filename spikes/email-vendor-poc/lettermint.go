package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const lettermintSendURL = "https://api.lettermint.co/v1/send"

// lettermintSendRequest is the JSON body for POST /v1/send. NEXT.md's
// documented shape said `to` was a plain string; the real API rejects that
// with a 422 ("The to field must be an array.") — corrected here against the
// real API's own validation error, not the docs (Lettermint's Go-guide docs
// page returned only the official SDK's fluent builder, not the raw wire
// format, so there was no docs page to check this against directly).
type lettermintSendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text,omitempty"`
	HTML    string   `json:"html,omitempty"`
}

type lettermintSendResponse struct {
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
}

// sendLettermint sends one email via Lettermint's REST API. Returns the
// parsed response and the raw response body (for logging/debugging when
// parsing fails or the shape differs from what's expected here).
func sendLettermint(token string, req lettermintSendRequest) (*lettermintSendResponse, []byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, lettermintSendURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-lettermint-token", token)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, respBody, fmt.Errorf("lettermint: unexpected status %d: %s", resp.StatusCode, respBody)
	}

	var out lettermintSendResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, respBody, fmt.Errorf("unmarshal response: %w (raw: %s)", err, respBody)
	}
	return &out, respBody, nil
}
