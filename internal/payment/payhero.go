// internal/payment/payhero.go
package payment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const PayHeroAPIBaseURL = "https://backend.payhero.co.ke"

var (
	ErrInvalidPhone = errors.New("invalid Kenyan phone number format")
	phoneRegex      = regexp.MustCompile(`^(?:254|\+254|0)?([17]\d{8})$`)
)

// NormalizeKenyanPhone standardizes numbers to 07xxxxxxxx or 01xxxxxxxx for PayHero.
func NormalizeKenyanPhone(raw string) (string, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")

	matches := phoneRegex.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return "", ErrInvalidPhone
	}
	return "0" + matches[1], nil
}

// Client interacts with PayHero's v2 REST API.
type Client struct {
	username   string
	password   string
	channelID  int
	httpClient *http.Client
}

// NewClient creates a new PayHero client.
func NewClient(username, password string, channelID int) *Client {
	return &Client{
		username:  username,
		password:  password,
		channelID: channelID,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// STKPushRequest payload according to PayHero v2 docs.
type STKPushRequest struct {
	Amount            int    `json:"amount"`
	PhoneNumber       string `json:"phone_number"`
	ChannelID         int    `json:"channel_id"`
	Provider          string `json:"provider"`
	ExternalReference string `json:"external_reference"`
	CallbackURL       string `json:"callback_url"`
}

// STKPushResponse from PayHero.
type STKPushResponse struct {
	Success   bool   `json:"success"`
	Status    string `json:"status"`
	Reference string `json:"reference"`
	Message   string `json:"message"`
}

// InitiateSTK sends an M-Pesa STK push prompt to the customer's phone.
func (c *Client) InitiateSTK(ctx context.Context, phone string, amount int, extRef, callbackURL string) (*STKPushResponse, error) {
	normPhone, err := NormalizeKenyanPhone(phone)
	if err != nil {
		return nil, err
	}

	reqBody := STKPushRequest{
		Amount:            amount,
		PhoneNumber:       normPhone,
		ChannelID:         c.channelID,
		Provider:          "m-pesa",
		ExternalReference: extRef,
		CallbackURL:       callbackURL,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/v2/payments", PayHeroAPIBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	// Basic Auth: base64(username:password)
	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", c.username, c.password)))
	req.Header.Set("Authorization", fmt.Sprintf("Basic %s", auth))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("payhero request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("payhero responded with error status: %d", resp.StatusCode)
	}

	var stkResp STKPushResponse
	if err := json.NewDecoder(resp.Body).Decode(&stkResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &stkResp, nil
}

// TransactionItem represents a transaction record from PayHero's API.
type TransactionItem struct {
	ID                int     `json:"id"`
	TransactionType   string  `json:"transaction_type"`
	Amount            float64 `json:"amount"`
	ProviderReference string  `json:"provider_reference"`
	ExternalReference string  `json:"external_reference"`
}

type transactionsListResponse struct {
	Transactions []TransactionItem `json:"transactions"`
}

// FindInboundPayment queries PayHero for a confirmed payment matching our external reference.
func (c *Client) FindInboundPayment(ctx context.Context, extRef string) (*TransactionItem, error) {
	endpoint := fmt.Sprintf("%s/api/v2/transactions?reference=%s", PayHeroAPIBaseURL, extRef)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", c.username, c.password)))
	req.Header.Set("Authorization", fmt.Sprintf("Basic %s", auth))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("payhero query failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("payhero query returned status: %d", resp.StatusCode)
	}

	var list transactionsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("failed to decode payhero transactions: %w", err)
	}

	for _, tx := range list.Transactions {
		if tx.ExternalReference == extRef && tx.TransactionType == "inbound_payment" {
			return &tx, nil
		}
	}

	return nil, nil // Still pending
}
