package gateways

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// FetcherConfig bounds a single fetch.
type FetcherConfig struct {
	Timeout      time.Duration
	MaxBytes     int64
	UserAgent    string
	AllowPrivate bool
}

// Fetcher fetches URLs with the guards the spec requires: only http/https, a real
// User-Agent, a timeout, a size ceiling and an SSRF guard. It is ad-hoc: one
// request, no client kept warm beyond the shared transport.
type Fetcher struct {
	cfg    FetcherConfig
	client *http.Client
}

var _ interfaces.Fetcher = (*Fetcher)(nil)

// NewFetcher builds a fetcher.
func NewFetcher(cfg FetcherConfig) *Fetcher {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 5 << 20
	}
	return &Fetcher{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

// Fetch downloads rawURL and returns the response. A non-2xx status is returned
// (not an error) so the caller can record "still exists, but 404".
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (interfaces.FetchedPage, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return interfaces.FetchedPage{}, types.New("invalid_url", "not a valid URL", types.ErrInvalidURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return interfaces.FetchedPage{}, types.New("invalid_url", "only http and https URLs are supported", types.ErrInvalidURL)
	}
	if !f.cfg.AllowPrivate {
		if err := guardPrivateHost(u.Hostname()); err != nil {
			return interfaces.FetchedPage{}, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return interfaces.FetchedPage{}, err
	}
	req.Header.Set("User-Agent", f.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return interfaces.FetchedPage{}, fmt.Errorf("fetch %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBytes))
	if err != nil {
		return interfaces.FetchedPage{}, fmt.Errorf("read %s: %w", u, err)
	}
	finalURL := u.String()
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return interfaces.FetchedPage{
		Status:       resp.StatusCode,
		ContentType:  resp.Header.Get("Content-Type"),
		Body:         body,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FinalURL:     finalURL,
		Wall:         detectWall(resp.StatusCode, finalURL, resp.Header.Get("Content-Type"), body),
	}, nil
}

func guardPrivateHost(host string) error {
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return types.New("forbidden", "refusing to fetch a private, loopback or link-local address", types.ErrForbidden)
		}
	}
	return nil
}
