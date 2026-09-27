package youtube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	maxScriptBytes  = 10 << 20 // 10 MB maximum player script size
	defaultBaseHost = "https://www.youtube.com"
)

var (
	// Discovery patterns for player script URL in watch-page HTML/JSON.
	playerJSURLPattern1 = regexp.MustCompile(`"(?:PLAYER_JS_URL|jsUrl)"\s*:\s*"([^"]+)"`)
	playerJSURLPattern2 = regexp.MustCompile(`<script[^>]+src=["']([^"']*/s/player/[^"']+)["']`)
	playerVersionRegex  = regexp.MustCompile(`/s/player/([a-zA-Z0-9_-]+)/`)
)

// Explicit allowlist of supported YouTube player script hosts.
// Wildcards are intentionally not permitted to prevent lookalike subdomains.
var allowedScriptHosts = map[string]bool{
	"www.youtube.com":   true,
	"youtube.com":       true,
	"m.youtube.com":     true,
	"music.youtube.com": true,
}

// isAllowedScriptHost checks if host is an explicitly allowlisted YouTube host.
func isAllowedScriptHost(host string) bool {
	return allowedScriptHosts[strings.ToLower(strings.TrimSpace(host))]
}

// DiscoverPlayerScriptURL searches watch-page HTML for the player script URL.
func DiscoverPlayerScriptURL(page []byte) (string, error) {
	pageStr := string(page)

	// Unescape forward slashes commonly escaped in JSON: \/ -> /
	pageStr = strings.ReplaceAll(pageStr, `\/`, `/`)

	if match := playerJSURLPattern1.FindStringSubmatch(pageStr); len(match) > 1 {
		raw := strings.TrimSpace(match[1])
		if raw != "" {
			return resolveScriptURL(raw)
		}
	}

	if match := playerJSURLPattern2.FindStringSubmatch(pageStr); len(match) > 1 {
		raw := strings.TrimSpace(match[1])
		if raw != "" {
			return resolveScriptURL(raw)
		}
	}

	return "", errors.New("youtube: player script URL not found in watch page")
}

// resolveScriptURL validates and resolves a discovered player script URL.
func resolveScriptURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("youtube: empty player script URL")
	}

	var parsed *url.URL
	var err error

	if strings.HasPrefix(raw, "//") {
		parsed, err = url.Parse("https:" + raw)
	} else if strings.HasPrefix(raw, "/") {
		parsed, err = url.Parse(defaultBaseHost + raw)
	} else {
		parsed, err = url.Parse(raw)
	}

	if err != nil {
		return "", fmt.Errorf("youtube: invalid player script URL: %w", err)
	}

	if err := ValidatePlayerScriptURL(parsed); err != nil {
		return "", err
	}

	return parsed.String(), nil
}

// ValidatePlayerScriptURL enforces HTTPS, host allowlist, credential rejection, and path prefix.
func ValidatePlayerScriptURL(u *url.URL) error {
	if u == nil {
		return errors.New("youtube: nil script URL")
	}

	if u.Scheme != "https" {
		return errors.New("youtube: player script URL must use HTTPS")
	}

	if u.User != nil {
		return errors.New("youtube: player script URL must not contain credentials")
	}

	if u.Port() != "" {
		return fmt.Errorf("youtube: explicit port %q not allowed in player script URL", u.Port())
	}

	hostname := strings.ToLower(u.Hostname())
	if !isAllowedScriptHost(hostname) {
		return fmt.Errorf("youtube: untrusted player script host %q", hostname)
	}

	path := u.Path
	if !strings.HasPrefix(path, "/s/player/") {
		return fmt.Errorf("youtube: player script URL path %q must start with /s/player/", path)
	}

	return nil
}

// ExtractPlayerVersion returns a version or hash identifier extracted from the script URL path.
func ExtractPlayerVersion(scriptURL string) string {
	if match := playerVersionRegex.FindStringSubmatch(scriptURL); len(match) > 1 {
		return match[1]
	}
	if parsed, err := url.Parse(scriptURL); err == nil {
		return parsed.Path
	}
	return scriptURL
}

// fetchPlayerScript downloads the player JavaScript code using bounded reader and redirect checks.
func (e *Extractor) fetchPlayerScript(ctx context.Context, scriptURL string) (PlayerScript, error) {
	parsed, err := url.Parse(scriptURL)
	if err != nil {
		return PlayerScript{}, fmt.Errorf("youtube: parse script URL: %w", err)
	}

	if err := ValidatePlayerScriptURL(parsed); err != nil {
		return PlayerScript{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return PlayerScript{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")

	// Create client that validates redirect hops
	client := e.client
	if client == nil {
		client = http.DefaultClient
	}

	// Make a shallow copy of client to apply custom CheckRedirect
	redirectClient := *client
	redirectClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("youtube: stopped after 10 redirects")
		}
		return ValidatePlayerScriptURL(req.URL)
	}

	resp, err := redirectClient.Do(req)
	if err != nil {
		return PlayerScript{}, fmt.Errorf("youtube: fetch player script: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return PlayerScript{}, fmt.Errorf("youtube: player script request returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxScriptBytes+1))
	if err != nil {
		return PlayerScript{}, fmt.Errorf("youtube: read player script: %w", err)
	}

	if len(body) > maxScriptBytes {
		return PlayerScript{}, errors.New("youtube: player script exceeds size limit")
	}

	if err := ctx.Err(); err != nil {
		return PlayerScript{}, err
	}

	versionID := ExtractPlayerVersion(scriptURL)

	return NewPlayerScript(parsed.String(), versionID, body), nil
}
