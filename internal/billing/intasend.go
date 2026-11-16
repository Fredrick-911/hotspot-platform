// internal/billing/intasend.go
package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://api.intasend.com"
	defaultTimeout = 15 * time.Second
)

var (
	ErrInvalidPhone = errors.New("invalid Kenyan phone number for IntaSend")
	ErrAPIError     = errors.New("intasend API error")
)

// Client interacts with IntaSend REST API.
type Client struct {
	BaseURL        string
	PublishableKey string
	SecretKey      string
	HTTPClient     *http.Client
}

// NewClient returns an initialized IntaSend client.
func NewClient(publishableKey, secretKey string) *Client {
	return &Client{
		BaseURL:        DefaultBaseURL,
		PublishableKey: publishableKey,
		SecretKey:      secretKey,
		HTTPClient:     &http.Client{Timeout: defaultTimeout},
	}
}

// NormalizeKenyanPhone standardizes phone numbers to 2547XXXXXXXX or 2541XXXXXXXX as required by IntaSend.
var phoneRe = regexp.MustCompile(`^\d{9,12}$`)

func NormalizeKenyanPhone(input string) (string, error) {
	digits := regexp.MustCompile(`\D`).ReplaceAllString(input, "")
	if !phoneRe.MatchString(digits) {
		return "", ErrInvalidPhone
	}

	switch {
	case strings.HasPrefix(digits, "254") && len(digits) == 12:
		return digits, nil
	case strings.HasPrefix(digits, "0") && len(digits) == 10:
		return "254" + digits[1:], nil
	case (strings.HasPrefix(digits, "7") || strings.HasPrefix(digits, "1")) && len(digits) == 9:
		return "254" + digits, nil
	default:
		return "", ErrInvalidPhone
	}
}

// STKPushRequest payload for M-Pesa STK Push.
type STKPushRequest struct {
	Amount      int    `json:"amount"`
	PhoneNumber string `json:"phone_number"`
	APIRef      string `json:"api_ref,omitempty"`
}

// STKPushResponse from IntaSend STK Push.
type STKPushResponse struct {
	ID      string `json:"id"`
	Invoice struct {
		InvoiceID string  `json:"invoice_id"`
		State     string  `json:"state"`
		Provider  string  `json:"provider"`
		Value     float64 `json:"value"`
		Account   string  `json:"account"`
		APIRef    string  `json:"api_ref"`
	} `json:"invoice"`
	Customer struct {
		CustomerID  string `json:"customer_id"`
		PhoneNumber string `json:"phone_number"`
	} `json:"customer"`
}

// SendSTKPush triggers an M-Pesa STK push prompt to the box owner's phone.
func (c *Client) SendSTKPush(ctx context.Context, amount int, phone, apiRef string) (*STKPushResponse, error) {
	normPhone, err := NormalizeKenyanPhone(phone)
	if err != nil {
		return nil, err
	}

	reqBody, err := json.Marshal(STKPushRequest{
		Amount:      amount,
		PhoneNumber: normPhone,
		APIRef:      apiRef,
	})
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/api/v1/payment/mpesa-stk-push/", strings.TrimRight(c.BaseURL, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.SecretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("intasend stk request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d", ErrAPIError, resp.StatusCode)
	}

	var res STKPushResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode stk response: %w", err)
	}

	return &res, nil
}

// CheckoutRequest creates a hosted payment page for Card, M-Pesa, Bank, or Apple Pay.
type CheckoutRequest struct {
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Email       string  `json:"email,omitempty"`
	FirstName   string  `json:"first_name,omitempty"`
	LastName    string  `json:"last_name,omitempty"`
	PhoneNumber string  `json:"phone_number,omitempty"`
	APIRef      string  `json:"api_ref,omitempty"`
	RedirectURL string  `json:"redirect_url,omitempty"`
}

// CheckoutResponse contains the URL where the tenant is redirected to complete payment.
type CheckoutResponse struct {
	ID        string  `json:"id"`
	URL       string  `json:"url"`
	Signature string  `json:"signature"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Paid      bool    `json:"paid"`
}

// CreateCheckout initiates an IntaSend hosted checkout session.
func (c *Client) CreateCheckout(ctx context.Context, cr CheckoutRequest) (*CheckoutResponse, error) {
	if cr.Currency == "" {
		cr.Currency = "KES"
	}
	if cr.PhoneNumber != "" {
		if norm, err := NormalizeKenyanPhone(cr.PhoneNumber); err == nil {
			cr.PhoneNumber = norm
		}
	}

	reqBody, err := json.Marshal(cr)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/api/v1/checkout/", strings.TrimRight(c.BaseURL, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-IntaSend-Public-API-Key", c.PublishableKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("intasend checkout request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d", ErrAPIError, resp.StatusCode)
	}

	var res CheckoutResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode checkout response: %w", err)
	}

	return &res, nil
}

// PaymentStatusResponse from checking an invoice.
type PaymentStatusResponse struct {
	Invoice struct {
		InvoiceID    string  `json:"invoice_id"`
		State        string  `json:"state"`
		Provider     string  `json:"provider"`
		Value        float64 `json:"value"`
		Charges      float64 `json:"charges"`
		NetAmount    float64 `json:"net_amount"`
		Currency     string  `json:"currency"`
		Account      string  `json:"account"`
		APIRef       string  `json:"api_ref"`
		ProviderRef  *string `json:"provider_ref"`
		FailedReason *string `json:"failed_reason"`
		FailedCode   *int    `json:"failed_code"`
	} `json:"invoice"`
}

// CheckPaymentStatus queries IntaSend for the current state of an invoice.
func (c *Client) CheckPaymentStatus(ctx context.Context, invoiceID string) (*PaymentStatusResponse, error) {
	payload, err := json.Marshal(map[string]string{"invoice_id": invoiceID})
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/api/v1/payment/status/", strings.TrimRight(c.BaseURL, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.SecretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("status check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d", ErrAPIError, resp.StatusCode)
	}

	var res PaymentStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode status response: %w", err)
	}

	return &res, nil
}
