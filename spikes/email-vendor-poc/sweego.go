package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const sweegoSendURL = "https://api.sweego.io/send"

// sweegoAddress is the {email, name} shape Sweego's API uses for from/recipients.
type sweegoAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// sweegoSendRequest is the JSON body for POST /send, confirmed against
// Sweego's real (JS-rendered) API reference via a browser render — WebFetch
// could not render this site (returned only the nav shell on 5 separate
// attempts across different pages); a real browser tool was needed instead.
// That gap is itself a documentation-accessibility data point for REPORT.md.
type sweegoSendRequest struct {
	Channel     string          `json:"channel"`
	Provider    string          `json:"provider"`
	From        sweegoAddress   `json:"from"`
	Recipients  []sweegoAddress `json:"recipients"`
	Subject     string          `json:"subject"`
	MessageTxt  string          `json:"message-txt,omitempty"`
	MessageHTML string          `json:"message-html,omitempty"`
}

type sweegoSendResponse struct {
	CreditLeft    string         `json:"credit_left"`
	Channel       string         `json:"channel"`
	Provider      string         `json:"provider"`
	SwgUIDs       map[string]any `json:"swg_uids"`
	TransactionID string         `json:"transaction_id"`
}

// sendSweego sends one email via Sweego's REST API (POST /send). Auth is a
// single `Api-Key` header — not Bearer, not SMTP AUTH (both were tried first
// and rejected; the docs' own dedicated auth pages confirmed API-Key is the
// correct scheme for send/* routes). provider is fixed to "sweego" per the
// documented example — Sweego's own default provider value, not a per-call
// choice this spike needs to make.
func sendSweego(apiKey string, req sweegoSendRequest) (*sweegoSendResponse, []byte, error) {
	if req.Provider == "" {
		req.Provider = "sweego"
	}
	if req.Channel == "" {
		req.Channel = "email"
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, sweegoSendURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Api-Key", apiKey)

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
		return nil, respBody, fmt.Errorf("sweego: unexpected status %d: %s", resp.StatusCode, respBody)
	}

	var out sweegoSendResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, respBody, fmt.Errorf("unmarshal response: %w (raw: %s)", err, respBody)
	}
	return &out, respBody, nil
}
