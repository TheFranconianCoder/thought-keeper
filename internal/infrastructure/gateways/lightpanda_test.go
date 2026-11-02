package gateways

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

type fakeToolRunner struct {
	available bool
	out       []byte
	err       error
	name      string
	args      []string
}

func (f *fakeToolRunner) Available(string) bool { return f.available }

func (f *fakeToolRunner) Run(_ context.Context, name string, args []string, _ []byte) ([]byte, error) {
	f.name, f.args = name, args
	return f.out, f.err
}

const renderedJSON = `{"url":"https://example.com/page","http_status":200,` +
	`"headers":[{"name":"Content-Type","value":"text/html; charset=utf-8"}],` +
	`"dump":"html","content":"<html><body><p>rendered</p></body></html>",` +
	`"error":null,"bot_challenge":null}`

func newTestPanda(cfg LightpandaConfig, runner *fakeToolRunner) *Lightpanda {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30_000_000_000
	}
	cfg.MinTextChars = 400
	return NewLightpanda(cfg, runner)
}

// TestLightpandaIsOptional: without the binary the app has no renderer at all — that is the Windows
// case and a fresh install, and neither may stop a page from being read over plain HTTP.
func TestLightpandaIsOptional(t *testing.T) {
	if NewLightpanda(LightpandaConfig{}, &fakeToolRunner{available: false}) != nil {
		t.Fatalf("a missing binary produced a renderer")
	}
}

// TestLightpandaRendersTheDOM: the JSON is the contract — status, content type, final URL and the
// serialized DOM.
func TestLightpandaRendersTheDOM(t *testing.T) {
	runner := &fakeToolRunner{available: true, out: []byte(renderedJSON)}
	panda := newTestPanda(LightpandaConfig{}, runner)
	page, err := panda.Render(context.Background(), "https://example.com/page")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if page.Status != 200 || page.ContentType != "text/html; charset=utf-8" {
		t.Errorf("page = %+v", page)
	}
	if page.FinalURL != "https://example.com/page" || !strings.Contains(string(page.HTML), "rendered") {
		t.Errorf("page = %+v", page)
	}
	if page.Engine != EngineLightpanda {
		t.Errorf("engine = %q", page.Engine)
	}
	if runner.name != "lightpanda" {
		t.Errorf("binary = %q", runner.name)
	}
}

// TestLightpandaArgsCarryTheGuards: the SSRF block, the size ceiling and the identity are flags, so a
// rendered fetch is as guarded as a plain one.
func TestLightpandaArgsCarryTheGuards(t *testing.T) {
	runner := &fakeToolRunner{available: true, out: []byte(renderedJSON)}
	panda := newTestPanda(LightpandaConfig{
		Path: "/opt/lightpanda", MaxBytes: 2048, UserAgent: "thought-keeper/0.1 (+local)",
		LoadStylesheets: true, ObeyRobots: true,
	}, runner)
	if _, err := panda.Render(context.Background(), "https://example.com/page"); err != nil {
		t.Fatalf("render: %v", err)
	}
	if runner.name != "/opt/lightpanda" {
		t.Errorf("binary = %q, want the configured path", runner.name)
	}
	joined := strings.Join(runner.args, " ")
	for _, want := range []string{
		"fetch", "--dump html", "--json",
		"--dump-max-bytes 2048",
		"--wait-until networkidle",
		"--block-private-networks",
		"--user-agent thought-keeper/0.1 (+local)",
		"--obey-robots",
		"--load-resources stylesheet",
		"https://example.com/page",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %s", want, joined)
		}
	}
}

// TestLightpandaWaitUntil: the dump must wait for the data a script fetched — `load` is the shell of a
// single-page app. An unknown value falls back to networkidle instead of failing every page.
func TestLightpandaWaitUntil(t *testing.T) {
	runner := &fakeToolRunner{available: true, out: []byte(renderedJSON)}
	panda := newTestPanda(LightpandaConfig{WaitUntil: "load"}, runner)
	if _, err := panda.Render(context.Background(), "https://example.com/page"); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(strings.Join(runner.args, " "), "--wait-until load") {
		t.Errorf("a configured wait event was not passed: %v", runner.args)
	}

	runner2 := &fakeToolRunner{available: true, out: []byte(renderedJSON)}
	panda2 := newTestPanda(LightpandaConfig{WaitUntil: "nonsense"}, runner2)
	if _, err := panda2.Render(context.Background(), "https://example.com/page"); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(strings.Join(runner2.args, " "), "--wait-until networkidle") {
		t.Errorf("an unknown wait event did not fall back: %v", runner2.args)
	}
}

// TestLightpandaDropsAMozillaUserAgent: the engine refuses to impersonate a browser, and a
// configured UA must not break every page because of it.
func TestLightpandaDropsAMozillaUserAgent(t *testing.T) {
	runner := &fakeToolRunner{available: true, out: []byte(renderedJSON)}
	panda := newTestPanda(LightpandaConfig{UserAgent: "Mozilla/5.0 (X11; Linux x86_64)"}, runner)
	if _, err := panda.Render(context.Background(), "https://example.com/page"); err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(strings.Join(runner.args, " "), "--user-agent") {
		t.Errorf("a Mozilla user agent was passed: %v", runner.args)
	}
}

// TestLightpandaNamesTheNavigationFailure: a failed navigation still prints JSON, and that JSON is the
// better error — it says what went wrong instead of "exit status 1".
func TestLightpandaNamesTheNavigationFailure(t *testing.T) {
	runner := &fakeToolRunner{
		available: true,
		out:       []byte(`{"url":"https://example.com","http_status":0,"content":"","error":"navigation_error"}`),
		err:       errors.New("exit status 1"),
	}
	panda := newTestPanda(LightpandaConfig{}, runner)
	_, err := panda.Render(context.Background(), "https://example.com")
	if err == nil || !strings.Contains(err.Error(), "navigation_error") {
		t.Fatalf("err = %v, want the navigation failure", err)
	}
}

// TestLightpandaExemptsItsProxy: the private-network block applies after DNS resolution, so the HTTP
// proxy the environment configured would be blocked too. The guard lets exactly that host through.
func TestLightpandaExemptsItsProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://10.1.2.3:3128")
	runner := &fakeToolRunner{available: true, out: []byte(renderedJSON)}
	panda := newTestPanda(LightpandaConfig{}, runner)
	if _, err := panda.Render(context.Background(), "https://example.com/page"); err != nil {
		t.Fatalf("render: %v", err)
	}
	joined := strings.Join(runner.args, " ")
	if !strings.Contains(joined, "--block-private-networks") {
		t.Errorf("the SSRF guard was dropped: %s", joined)
	}
	if !strings.Contains(joined, "--block-cidrs -10.1.2.3/32") {
		t.Errorf("the configured proxy is not exempt: %s", joined)
	}
}

// TestLightpandaDetectsARenderedWall: a login gate that only exists after hydration must still be
// recognised as a gate; the browser sees it even when the plain response did not.
func TestLightpandaDetectsARenderedWall(t *testing.T) {
	body := `<html><body><form><input type="password" name="pw"></form><p>Sign in to continue.</p></body></html>`
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	runner := &fakeToolRunner{available: true, out: []byte(
		`{"url":"https://example.com/member","http_status":200,` +
			`"headers":[{"name":"content-type","value":"text/html"}],` +
			`"content":` + string(encoded) + `,"error":null}`)}
	panda := newTestPanda(LightpandaConfig{}, runner)
	page, err := panda.Render(context.Background(), "https://example.com/member")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if page.Wall != entities.WallLogin {
		t.Errorf("wall = %q, want %q", page.Wall, entities.WallLogin)
	}
}
