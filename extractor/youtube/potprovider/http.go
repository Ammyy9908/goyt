package potprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ammyy9908/goyt/extractor/youtube"
)

const (
	// DefaultTimeout is the default HTTP request timeout for PO-token generation requests.
	DefaultTimeout = 10 * time.Second

	// MaxResponseBytes bounds the maximum readable response payload from the provider.
	MaxResponseBytes = 64 * 1024 // 64 KB
)

// HTTPOption configures an HTTPProvider.
type HTTPOption func(*HTTPProvider)

// WithHTTPClient configures a custom HTTP client on the HTTPProvider.
func WithHTTPClient(client *http.Client) HTTPOption {
	return func(p *HTTPProvider) {
		if client != nil {
			p.client = client
		}
	}
}

// WithTimeout configures the request timeout on the HTTPProvider.
func WithTimeout(d time.Duration) HTTPOption {
	return func(p *HTTPProvider) {
		if d > 0 {
			p.timeout = d
		}
	}
}

// HTTPProvider implements youtube.POTokenProvider for HTTP-based external PO-token generators
// (e.g. Brainicism/bgutil-ytdlp-pot-provider, jim60105/bgutil-ytdlp-pot-provider-rs).
//
// Implementations are safe for concurrent use across multiple goroutines.
type HTTPProvider struct {
	endpoint string
	client   *http.Client
	timeout  time.Duration
}

var _ youtube.POTokenProvider = (*HTTPProvider)(nil)

// ValidateEndpoint checks that the provider endpoint is a valid URL, prohibits credentials,
// and enforces loopback/local addressing for unencrypted HTTP transport.
func ValidateEndpoint(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("potprovider: endpoint URL is empty")
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("potprovider: invalid endpoint URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("potprovider: endpoint scheme %q must be http or https", u.Scheme)
	}

	if u.User != nil {
		return nil, errors.New("potprovider: endpoint URL with credentials is not permitted")
	}

	host := u.Hostname()
	if host == "" {
		return nil, errors.New("potprovider: endpoint URL missing hostname")
	}

	// For unencrypted HTTP, enforce local/loopback destination to prevent plain-text
	// token exposure across external networks. Remote providers must use HTTPS.
	if u.Scheme == "http" && !isLoopbackOrLocal(host) {
		return nil, fmt.Errorf("potprovider: unencrypted HTTP endpoint host %q must be localhost/loopback; remote endpoints require HTTPS", host)
	}

	return u, nil
}

func isLoopbackOrLocal(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "[::1]" {
		return true
	}
	ip := net.ParseIP(h)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// NewHTTPProvider creates a new HTTP-based PO-token provider targeting the specified endpoint.
func NewHTTPProvider(endpoint string, opts ...HTTPOption) (*HTTPProvider, error) {
	u, err := ValidateEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	// Canonical base URL without trailing slash
	baseURL := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	if u.Path != "" && u.Path != "/" {
		baseURL += strings.TrimRight(u.Path, "/")
	}

	clonedClient := &http.Client{
		Transport: http.DefaultTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errors.New("potprovider: HTTP redirects are prohibited for token provider endpoint")
		},
		Timeout: DefaultTimeout,
	}

	p := &HTTPProvider{
		endpoint: baseURL,
		client:   clonedClient,
		timeout:  DefaultTimeout,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p, nil
}

// Endpoint returns the configured canonical base URL of the provider.
func (p *HTTPProvider) Endpoint() string {
	return p.endpoint
}

type getPOTRequestBody struct {
	ContentBinding string `json:"content_binding,omitempty"`
}

type getPOTResponseBody struct {
	POToken        string `json:"poToken"`
	POTokenAlt     string `json:"po_token"`
	ContentBinding string `json:"contentBinding"`
	ContentBindAlt string `json:"content_binding"`
	ExpiresAtStr   string `json:"expiresAt"`
	ExpiresAtAlt   string `json:"expires_at"`
}

// GetPOToken requests a Proof-of-Origin token from the configured HTTP provider for the specified scope.
func (p *HTTPProvider) GetPOToken(ctx context.Context, req youtube.POTokenRequest) (youtube.POTokenResult, error) {
	if err := req.Validate(); err != nil {
		return youtube.POTokenResult{}, err
	}

	reqTimeout := p.timeout
	if reqTimeout <= 0 {
		reqTimeout = DefaultTimeout
	}

	callCtx, cancel := context.WithTimeout(ctx, reqTimeout)
	defer cancel()

	var binding string
	if req.Context == youtube.POTokenContextGVS {
		binding = req.VideoID
	} else if req.Context == youtube.POTokenContextPlayer {
		binding = req.VisitorData
	}

	reqBody := getPOTRequestBody{
		ContentBinding: binding,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return youtube.POTokenResult{}, fmt.Errorf("potprovider: failed to serialize request: %w", err)
	}

	targetURL := p.endpoint + "/get_pot"
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, targetURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return youtube.POTokenResult{}, fmt.Errorf("potprovider: failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		if callCtx.Err() != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return youtube.POTokenResult{}, context.Canceled
			}
			return youtube.POTokenResult{}, context.DeadlineExceeded
		}
		return youtube.POTokenResult{}, fmt.Errorf("%w: HTTP request failed: %v", youtube.ErrPOTokenProviderFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return youtube.POTokenResult{}, youtube.ErrPOTokenUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return youtube.POTokenResult{}, fmt.Errorf("%w: provider returned HTTP %d", youtube.ErrPOTokenProviderFailed, resp.StatusCode)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return youtube.POTokenResult{}, fmt.Errorf("%w: failed to read response: %v", youtube.ErrPOTokenProviderFailed, err)
	}
	if len(respBody) > MaxResponseBytes {
		return youtube.POTokenResult{}, fmt.Errorf("%w: response exceeds maximum size limit", youtube.ErrPOTokenProviderFailed)
	}

	var parsedResp getPOTResponseBody
	if err := json.Unmarshal(respBody, &parsedResp); err != nil {
		return youtube.POTokenResult{}, fmt.Errorf("%w: invalid JSON response from provider", youtube.ErrPOTokenProviderFailed)
	}

	token := strings.TrimSpace(parsedResp.POToken)
	if token == "" {
		token = strings.TrimSpace(parsedResp.POTokenAlt)
	}
	if token == "" {
		return youtube.POTokenResult{}, youtube.ErrPOTokenInvalid
	}

	var exp *time.Time
	expStr := strings.TrimSpace(parsedResp.ExpiresAtStr)
	if expStr == "" {
		expStr = strings.TrimSpace(parsedResp.ExpiresAtAlt)
	}
	if expStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, expStr); err == nil {
			exp = &t
		} else if t, err := time.Parse(time.RFC3339, expStr); err == nil {
			exp = &t
		}
	}

	returnedBinding := strings.TrimSpace(parsedResp.ContentBinding)
	if returnedBinding == "" {
		returnedBinding = strings.TrimSpace(parsedResp.ContentBindAlt)
	}

	if binding != "" && returnedBinding != "" && returnedBinding != binding {
		return youtube.POTokenResult{}, fmt.Errorf("%w: requested content_binding %q but received %q", youtube.ErrPOTokenScopeMismatch, binding, returnedBinding)
	}

	var returnedVisitor string
	if req.Context == youtube.POTokenContextPlayer {
		if returnedBinding != "" {
			returnedVisitor = returnedBinding
		} else {
			returnedVisitor = req.VisitorData
		}
	} else {
		returnedVisitor = req.VisitorData
	}

	result := youtube.POTokenResult{
		Token:       token,
		Client:      req.Client,
		Context:     req.Context,
		VideoID:     req.VideoID,
		VisitorData: returnedVisitor,
		ExpiresAt:   exp,
	}

	if err := result.ValidateFor(req, time.Now()); err != nil {
		return youtube.POTokenResult{}, err
	}

	return result, nil
}

// Ping sends a lightweight health check to the provider's /ping endpoint.
func (p *HTTPProvider) Ping(ctx context.Context) error {
	reqTimeout := p.timeout
	if reqTimeout <= 0 {
		reqTimeout = DefaultTimeout
	}

	callCtx, cancel := context.WithTimeout(ctx, reqTimeout)
	defer cancel()

	targetURL := p.endpoint + "/ping"
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("potprovider: failed to create ping request: %w", err)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		if callCtx.Err() != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return context.Canceled
			}
			return context.DeadlineExceeded
		}
		return fmt.Errorf("potprovider: ping failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("potprovider: ping returned HTTP %d", resp.StatusCode)
	}

	return nil
}
