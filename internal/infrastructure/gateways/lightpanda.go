package gateways

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// EngineLightpanda is the name of the one browser engine the app knows. It is written into the
// snapshot's provenance, so the record names what rendered the page.
const EngineLightpanda = "lightpanda"

// LightpandaConfig bounds one rendered fetch.
type LightpandaConfig struct {
	// Path is the binary. Empty means "lightpanda from PATH".
	Path string
	// Timeout is the ceiling for the whole run: process start, navigation and the dump. It also
	// bounds the browser's own HTTP transfer and JavaScript execution.
	Timeout time.Duration
	// Wait is how long the page may take after loading before the dump is taken.
	Wait time.Duration
	// WaitUntil is the event the dump waits for: "load" (the shell of a single-page app),
	// "networkidle" (the data a script fetched has arrived) or "done". Empty means networkidle —
	// the whole point of rendering is the DOM a reader sees, not the one the server sent.
	WaitUntil string
	// MaxBytes caps the dumped DOM (Lightpanda truncates at a UTF-8 boundary and says so).
	MaxBytes  int64
	UserAgent string
	// AllowPrivate turns OFF the private-network block. It mirrors fetch.allow_private: the same
	// guard, enforced inside the browser as well as by the resolver.
	AllowPrivate bool
	// ObeyRobots lets the browser honour robots.txt. Off by default: the human asked for this one
	// page, and the app never crawls on its own.
	ObeyRobots bool
	// LoadStylesheets makes the browser fetch CSS, so display:none/visibility hiding is computed
	// the way a reader's browser computes it. It costs requests; images and iframes stay off.
	LoadStylesheets bool
	// MinTextChars is the article-text floor `detectWall` uses for the rendered document.
	MinTextChars int
}

// Lightpanda renders a URL with the Lightpanda headless browser, one process per page: no daemon,
// no port, no session, no cookie jar. The process is isolated (a crash costs one page) and its
// peak memory is about 35 MB.
type Lightpanda struct {
	cfg    LightpandaConfig
	runner interfaces.ToolRunner
	name   string

	proxyOnce sync.Once
	proxyIPs  []string
}

var _ interfaces.Renderer = (*Lightpanda)(nil)

// NewLightpanda builds the renderer. It returns nil when the binary is not there: the browser is an
// optional tool like poppler and tesseract, never a reason for the app not to start.
func NewLightpanda(cfg LightpandaConfig, runner interfaces.ToolRunner) *Lightpanda {
	name := strings.TrimSpace(cfg.Path)
	if name == "" {
		name = EngineLightpanda
	}
	if runner == nil || !runner.Available(name) {
		return nil
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Wait <= 0 {
		cfg.Wait = 5 * time.Second
	}
	if cfg.WaitUntil == "" {
		cfg.WaitUntil = "networkidle"
	}
	if !slices.Contains([]string{"load", "domcontentloaded", "networkalmostidle", "networkidle", "done"}, cfg.WaitUntil) {
		cfg.WaitUntil = "networkidle"
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 5 << 20
	}
	if cfg.MinTextChars <= 0 {
		cfg.MinTextChars = 400
	}
	return &Lightpanda{cfg: cfg, runner: runner, name: name}
}

// Name is the engine label written into the snapshot.
func (l *Lightpanda) Name() string { return EngineLightpanda }

// lightpandaHeader is one response header of the final document.
type lightpandaHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// lightpandaResult is the object `fetch --json` prints for one URL. `error` and `bot_challenge` are
// null when they do not apply, which decodes into the zero value.
type lightpandaResult struct {
	URL          string             `json:"url"`
	Status       int                `json:"http_status"`
	Headers      []lightpandaHeader `json:"headers"`
	Dump         string             `json:"dump"`
	Content      string             `json:"content"`
	Error        string             `json:"error"`
	BotChallenge string             `json:"bot_challenge"`
}

// Render loads rawURL in Lightpanda and returns the serialized DOM.
func (l *Lightpanda) Render(ctx context.Context, rawURL string) (interfaces.RenderedPage, error) {
	runCtx, cancel := context.WithTimeout(ctx, l.cfg.Timeout)
	defer cancel()
	out, err := l.runner.Run(runCtx, l.name, l.args(rawURL), nil)

	// A failed navigation still prints its JSON, and the JSON is the better error: it names the
	// failure ("navigation_error") instead of "exit status 1".
	var res lightpandaResult
	if parseErr := json.Unmarshal(out, &res); parseErr != nil {
		if err != nil {
			return interfaces.RenderedPage{}, fmt.Errorf("lightpanda: %w", err)
		}
		return interfaces.RenderedPage{}, fmt.Errorf("lightpanda: unreadable output: %w", parseErr)
	}
	if res.Error != "" {
		return interfaces.RenderedPage{}, fmt.Errorf("lightpanda: %s", res.Error)
	}
	if err != nil {
		return interfaces.RenderedPage{}, fmt.Errorf("lightpanda: %w", err)
	}
	if strings.TrimSpace(res.Content) == "" {
		return interfaces.RenderedPage{}, fmt.Errorf("lightpanda: %s gave no document", rawURL)
	}
	finalURL := res.URL
	if finalURL == "" {
		finalURL = rawURL
	}
	contentType := lastHeader(res.Headers, "content-type")
	html := []byte(res.Content)
	return interfaces.RenderedPage{
		Status:      res.Status,
		FinalURL:    finalURL,
		ContentType: contentType,
		HTML:        html,
		Wall:        detectWall(res.Status, finalURL, contentType, html, l.cfg.MinTextChars),
		Engine:      EngineLightpanda,
		Challenge:   res.BotChallenge,
	}, nil
}

// args builds the command line. Everything the app cares about is a flag here: the SSRF guard, the
// size ceiling, the two timeouts, the identity — the browser is a tool, not a second configuration.
func (l *Lightpanda) args(rawURL string) []string {
	args := []string{
		"fetch",
		"--dump", "html",
		"--json",
		"--dump-max-bytes", strconv.FormatInt(l.cfg.MaxBytes, 10),
		"--wait-until", l.cfg.WaitUntil,
		"--wait-ms", strconv.FormatInt(l.cfg.Wait.Milliseconds(), 10),
		"--http-timeout", strconv.FormatInt(l.cfg.Timeout.Milliseconds(), 10),
		"--terminate-ms", strconv.FormatInt(l.cfg.Timeout.Milliseconds(), 10),
	}
	// Lightpanda refuses to impersonate a browser ("Mozilla" in the User-Agent). A configured UA
	// that carries it is dropped for the render instead of failing every page: the identity is a
	// courtesy, the page is the point.
	if ua := strings.TrimSpace(l.cfg.UserAgent); ua != "" && !strings.Contains(strings.ToLower(ua), "mozilla") {
		args = append(args, "--user-agent", ua)
	}
	if !l.cfg.AllowPrivate {
		args = append(args, "--block-private-networks")
		// The block applies AFTER DNS resolution, which also covers the HTTP proxy this process was
		// told to use. A proxy on a private address (a corporate gateway, a sandbox) would then be
		// unreachable and every page would "fail to connect" — the guard has to exempt the one host
		// the environment named.
		for _, cidr := range l.proxyExemptions() {
			args = append(args, "--block-cidrs", "-"+cidr)
		}
	}
	if l.cfg.ObeyRobots {
		args = append(args, "--obey-robots")
	}
	if l.cfg.LoadStylesheets {
		args = append(args, "--load-resources", "stylesheet")
	}
	return append(args, rawURL)
}

// lastHeader is the value of the LAST matching header: a redirect chain reports every hop, and the
// final document is the one the converter reads.
func lastHeader(headers []lightpandaHeader, name string) string {
	for _, header := range slices.Backward(headers) {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}
	return ""
}

// proxyExemptions names the IPs of the HTTP(S) proxy the environment configured, as CIDR blocks the
// browser's private-network guard must let through. It is resolved once: the proxy does not move
// while the app runs, and a page must not pay for a DNS lookup of it. The lookup has its own short
// deadline, so a broken resolver costs two seconds once instead of every page.
func (l *Lightpanda) proxyExemptions() []string {
	l.proxyOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		seen := map[string]bool{}
		for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
			raw := strings.TrimSpace(os.Getenv(key))
			if raw == "" {
				continue
			}
			u, err := url.Parse(raw)
			if err != nil || u.Hostname() == "" {
				continue
			}
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				bits := 32
				if addr.IP.To4() == nil {
					bits = 128
				}
				cidr := fmt.Sprintf("%s/%d", addr.IP.String(), bits)
				if seen[cidr] {
					continue
				}
				seen[cidr] = true
				l.proxyIPs = append(l.proxyIPs, cidr)
			}
		}
	})
	return l.proxyIPs
}
