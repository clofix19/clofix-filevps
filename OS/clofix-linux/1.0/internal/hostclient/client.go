package hostclient

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Client talks to the trusted host-worker security API.
// Guests never receive broad kernel capabilities for these operations.
type Client struct {
	Endpoint  string
	TokenPath string
	HTTP      *http.Client
}

type Request struct {
	Action    string         `json:"action"`
	TenantID  string         `json:"tenant_id"`
	Nonce     string         `json:"nonce"`
	Timestamp int64          `json:"ts"`
	Payload   map[string]any `json:"payload"`
}

type Response struct {
	OK      bool           `json:"ok"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

func (c *Client) token() (string, error) {
	if c.TokenPath == "" {
		return "", fmt.Errorf("host token path not configured")
	}
	b, err := os.ReadFile(c.TokenPath)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(b)), nil
}

func (c *Client) Call(action, tenant string, payload map[string]any) (Response, error) {
	var resp Response
	if c.Endpoint == "" {
		return resp, fmt.Errorf("UNSUPPORTED: host API endpoint not configured (shared-kernel ops require host-worker)")
	}
	tok, err := c.token()
	if err != nil {
		return resp, err
	}
	req := Request{
		Action: action, TenantID: tenant,
		Nonce: fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp: time.Now().UTC().Unix(),
		Payload: payload,
	}
	body, _ := json.Marshal(req)
	mac := hmac.New(sha256.New, []byte(tok))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	httpReq, err := http.NewRequest(http.MethodPost, c.Endpoint+"/v1/security/"+action, bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Clofix-Signature", sig)
	httpReq.Header.Set("X-Clofix-Timestamp", fmt.Sprintf("%d", req.Timestamp))
	httpReq.Header.Set("X-Clofix-Nonce", req.Nonce)

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return resp, err
	}
	defer httpResp.Body.Close()
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return resp, err
	}
	return resp, nil
}
