package youtube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDiscoverPlayerScriptURL(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		want    string
		wantErr bool
	}{
		{
			name: "json player_js_url relative",
			html: `{"PLAYER_JS_URL":"/s/player/abcdef12/player_ias.vflset/en_US/base.js"}`,
			want: "https://www.youtube.com/s/player/abcdef12/player_ias.vflset/en_US/base.js",
		},
		{
			name: "json player_js_url escaped slashes",
			html: `{"PLAYER_JS_URL":"\/s\/player\/abcdef12\/player_ias.vflset\/en_US\/base.js"}`,
			want: "https://www.youtube.com/s/player/abcdef12/player_ias.vflset/en_US/base.js",
		},
		{
			name: "json jsUrl absolute https",
			html: `{"jsUrl":"https://www.youtube.com/s/player/12345678/player_ias.vflset/en_US/base.js"}`,
			want: "https://www.youtube.com/s/player/12345678/player_ias.vflset/en_US/base.js",
		},
		{
			name: "html script tag relative",
			html: `<html><head><script src="/s/player/fedcba98/player_ias.vflset/en_US/base.js"></script></head></html>`,
			want: "https://www.youtube.com/s/player/fedcba98/player_ias.vflset/en_US/base.js",
		},
		{
			name: "html script tag protocol relative",
			html: `<script src="//www.youtube.com/s/player/11223344/player_ias.vflset/en_US/base.js"></script>`,
			want: "https://www.youtube.com/s/player/11223344/player_ias.vflset/en_US/base.js",
		},
		{
			name:    "missing script url",
			html:    `<html><head><title>No player script</title></head></html>`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DiscoverPlayerScriptURL([]byte(tt.html))
			if (err != nil) != tt.wantErr {
				t.Fatalf("DiscoverPlayerScriptURL() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("DiscoverPlayerScriptURL() got = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidatePlayerScriptURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{
			name:    "valid www.youtube.com https",
			rawURL:  "https://www.youtube.com/s/player/abcdef/base.js",
			wantErr: false,
		},
		{
			name:    "valid m.youtube.com https",
			rawURL:  "https://m.youtube.com/s/player/abcdef/base.js",
			wantErr: false,
		},
		{
			name:    "valid music.youtube.com https",
			rawURL:  "https://music.youtube.com/s/player/abcdef/base.js",
			wantErr: false,
		},
		{
			name:    "reject explicit standard port 443",
			rawURL:  "https://www.youtube.com:443/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject explicit nonstandard port 8080",
			rawURL:  "https://www.youtube.com:8080/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject lookalike host ending with youtube.com",
			rawURL:  "https://youtube.com.attacker.com/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject lookalike host prefixed with youtube",
			rawURL:  "https://evil-youtube.com/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject arbitrary subdomain outside allowlist",
			rawURL:  "https://sub.youtube.com/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject http scheme",
			rawURL:  "http://www.youtube.com/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject credentials",
			rawURL:  "https://user:pass@www.youtube.com/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject username without password",
			rawURL:  "https://user@www.youtube.com/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject untrusted host",
			rawURL:  "https://evil.com/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject untrusted domain ending in youtube.com without dot",
			rawURL:  "https://notyoutube.com/s/player/abcdef/base.js",
			wantErr: true,
		},
		{
			name:    "reject invalid path prefix",
			rawURL:  "https://www.youtube.com/other/path/base.js",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidatePlayerScriptURL(parsed)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePlayerScriptURL(%q) error = %v, wantErr %v", tt.rawURL, err, tt.wantErr)
			}
		})
	}
}

func TestExtractPlayerVersion(t *testing.T) {
	url1 := "https://www.youtube.com/s/player/d78a63dc/player_ias.vflset/en_US/base.js"
	if v := ExtractPlayerVersion(url1); v != "d78a63dc" {
		t.Errorf("ExtractPlayerVersion(%q) = %q, want %q", url1, v, "d78a63dc")
	}

	url2 := "https://www.youtube.com/s/player/vfl999/base.js"
	if v := ExtractPlayerVersion(url2); v != "vfl999" {
		t.Errorf("ExtractPlayerVersion(%q) = %q, want %q", url2, v, "vfl999")
	}
}

func TestFetchPlayerScript_SecurityAndLimits(t *testing.T) {
	t.Run("successful script download", func(t *testing.T) {
		client := &http.Client{
			Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/s/player/v123/base.js" {
					t.Fatalf("unexpected path: %s", req.URL.Path)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/javascript"}},
					Body:       io.NopCloser(strings.NewReader("var testScript = 1;")),
					Request:    req,
				}, nil
			}),
		}

		ext := New(client)
		script, err := ext.fetchPlayerScript(context.Background(), "https://www.youtube.com/s/player/v123/base.js")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(script.Source) != "var testScript = 1;" {
			t.Fatalf("unexpected script source: %s", string(script.Source))
		}
		if script.VersionID != "v123" {
			t.Fatalf("unexpected version ID: %s", script.VersionID)
		}
	})

	t.Run("reject redirect to untrusted host", func(t *testing.T) {
		client := &http.Client{
			Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "www.youtube.com" {
					return &http.Response{
						StatusCode: http.StatusFound,
						Header: http.Header{
							"Location": []string{"https://attacker.com/s/player/v123/base.js"},
						},
						Body:    io.NopCloser(strings.NewReader("")),
						Request: req,
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("malicious code")),
					Request:    req,
				}, nil
			}),
		}

		ext := New(client)
		_, err := ext.fetchPlayerScript(context.Background(), "https://www.youtube.com/s/player/v123/base.js")
		if err == nil {
			t.Fatal("expected error on redirect to untrusted host, got nil")
		}
		if !strings.Contains(err.Error(), "untrusted") {
			t.Fatalf("expected untrusted host error, got: %v", err)
		}
	})

	t.Run("reject multi-hop redirect that eventually reaches untrusted host", func(t *testing.T) {
		client := &http.Client{
			Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "youtube.com" {
					// Hop 1: valid redirect from youtube.com to m.youtube.com
					return &http.Response{
						StatusCode: http.StatusFound,
						Header: http.Header{
							"Location": []string{"https://m.youtube.com/s/player/v123/base.js"},
						},
						Body:    io.NopCloser(strings.NewReader("")),
						Request: req,
					}, nil
				}
				if req.URL.Host == "m.youtube.com" {
					// Hop 2: malicious redirect to lookalike host
					return &http.Response{
						StatusCode: http.StatusFound,
						Header: http.Header{
							"Location": []string{"https://youtube.com.attacker.com/s/player/v123/base.js"},
						},
						Body:    io.NopCloser(strings.NewReader("")),
						Request: req,
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("malicious code")),
					Request:    req,
				}, nil
			}),
		}

		ext := New(client)
		_, err := ext.fetchPlayerScript(context.Background(), "https://youtube.com/s/player/v123/base.js")
		if err == nil {
			t.Fatal("expected error on multi-hop redirect to untrusted host, got nil")
		}
		if !strings.Contains(err.Error(), "untrusted") {
			t.Fatalf("expected untrusted host error, got: %v", err)
		}
	})

	t.Run("reject redirect to explicit port", func(t *testing.T) {
		client := &http.Client{
			Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "www.youtube.com" {
					return &http.Response{
						StatusCode: http.StatusFound,
						Header: http.Header{
							"Location": []string{"https://www.youtube.com:8443/s/player/v123/base.js"},
						},
						Body:    io.NopCloser(strings.NewReader("")),
						Request: req,
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("code")),
					Request:    req,
				}, nil
			}),
		}

		ext := New(client)
		_, err := ext.fetchPlayerScript(context.Background(), "https://www.youtube.com/s/player/v123/base.js")
		if err == nil {
			t.Fatal("expected error on redirect to port, got nil")
		}
		if !strings.Contains(err.Error(), "port") {
			t.Fatalf("expected port error, got: %v", err)
		}
	})

	t.Run("oversized script is rejected", func(t *testing.T) {
		client := &http.Client{
			Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
				bigBody := strings.Repeat("x", int(maxScriptBytes)+100)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(bigBody)),
					Request:    req,
				}, nil
			}),
		}

		ext := New(client)
		_, err := ext.fetchPlayerScript(context.Background(), "https://www.youtube.com/s/player/v123/base.js")
		if err == nil {
			t.Fatal("expected error for oversized script, got nil")
		}
		if !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("expected size limit error, got: %v", err)
		}
	})

	t.Run("http 404 response fails", func(t *testing.T) {
		client := &http.Client{
			Transport: playerTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("Not Found")),
					Request:    req,
				}, nil
			}),
		}

		ext := New(client)
		_, err := ext.fetchPlayerScript(context.Background(), "https://www.youtube.com/s/player/v123/base.js")
		if err == nil {
			t.Fatal("expected error for 404 script, got nil")
		}
	})

	t.Run("context cancellation interrupts fetch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		client := &http.Client{
			Timeout: 5 * time.Second,
		}

		ext := New(client)
		_, err := ext.fetchPlayerScript(ctx, "https://www.youtube.com/s/player/v123/base.js")
		if err == nil {
			t.Fatal("expected error for canceled context, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled error, got: %v", err)
		}
	})
}
