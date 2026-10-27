// Command thought-keeper is the desktop application and its CLI. Launching it (or
// `app`) starts the app; the other verbs are clients of the running instance.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/app"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/logging"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
	"github.com/TheFranconianCoder/thought-keeper/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// Load .env early (real environment wins) so client verbs see
	// THOUGHT_KEEPER_URL / OPENAI_* too.
	config.LoadDotEnv(config.DotEnvCandidates("")...)

	if len(args) == 0 {
		return cmdApp(nil)
	}
	switch args[0] {
	case "app", "serve":
		return cmdApp(args[1:])
	case "open":
		return cmdOpen(args[1:])
	case "status", "doctor":
		return cmdStatus(args[1:])
	case "add":
		return cmdAdd(args[1:])
	case "attach":
		return cmdAttach(args[1:])
	case "refetch":
		return cmdRefetch(args[1:])
	case "sources", "list-sources":
		return cmdSources(args[1:])
	case "tree":
		return cmdTree(args[1:])
	case "atoms":
		return cmdAtoms(args[1:])
	case "claim-topics", "branches":
		return cmdBranches(args[1:])
	case "distill":
		return cmdDistill(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "cluster":
		return cmdPost(args[1:], "/cluster", "cluster")
	case "models":
		return cmdModels(args[1:])
	case "search":
		return cmdSearch(args[1:])
	case "query":
		return cmdQuery(args[1:])
	case "lint":
		return cmdLint(args[1:])
	case "reset":
		return cmdReset(args[1:])
	case "reindex":
		return cmdPost(args[1:], "/reindex", "reindex")
	case "rebuild-trees":
		return cmdPost(args[1:], "/rebuild-trees", "rebuild-trees")
	case "quote-claim-code":
		return cmdPost(args[1:], "/atoms/quote-code", "quote-claim-code")
	case "version", "-version", "--version":
		fmt.Println(version.Full())
		return nil
	case "new-id":
		fmt.Println(ulid.Make().String())
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `thought-keeper — a local, agentic knowledge base

Usage:
  thought-keeper [app] [--headless] [--no-open] [--config PATH]   start the desktop app
  thought-keeper open                                 open the running instance's UI
  thought-keeper status [--json]                      show the doctor report
  thought-keeper add --path FILE | --url URL [--bookmark]   add a source
  thought-keeper attach --url URL --file FILE             attach a saved page to a URL the app could
                                                          not read (a login, a paywall); --source HASH
                                                          works too
  thought-keeper refetch (--source HASH | --url URL)      try the URL again for an attached snapshot;
                                                          a wall keeps the attachment
  thought-keeper sources [--kind K] [--limit N]       list sources
  thought-keeper tree --source HASH                   print a source's tree
  thought-keeper distill --source HASH                run a source's document steps (outline → distill); --reset replaces the derivation
  thought-keeper claim-topics                       list claim topics
  thought-keeper atoms [--topic T]                    list claims
  thought-keeper run --id ULID                        show an agent run
  thought-keeper cluster                              group claims (step 4)
  thought-keeper models [--config PATH]               list models from the LLM endpoint
  thought-keeper search --query Q [--layer atoms|sources]  lexical search
  thought-keeper query --question Q [--wait]          ask the claims (cited answer)
  thought-keeper lint [--wait]                            run health checks
  thought-keeper reindex                              rebuild the source index
  thought-keeper rebuild-trees                        rebuild every source tree
  thought-keeper quote-claim-code                     quote the bracket syntax in claim bodies as inline code
  thought-keeper new-id                               print one ULID
  thought-keeper version                              print the version

Client flags:
  --url URL    running instance address (default: from the runtime file, or THOUGHT_KEEPER_URL)
  --config P   config file (default: $XDG_CONFIG_HOME/thought-keeper/config.yaml)
`)
}

// apiRequest sends a request to the running instance and returns the status and body.
func apiRequest(addr, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+"/api/v1"+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("no running instance at %s: %w", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, nil
}

func cmdAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	path := fs.String("path", "", "filesystem path of the source")
	url := fs.String("url", "", "URL of the source")
	bookmark := fs.Bool("bookmark", false, "store the URL as a collected link (no indexing)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" && *url == "" {
		return errors.New("add requires --path or --url")
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	status, body, err := apiRequest(addr, http.MethodPost, "/add", payloads.AddRequest{Path: *path, URL: *url, Bookmark: *bookmark})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("add failed: %s", string(body))
	}
	var src payloads.Source
	if err := json.Unmarshal(body, &src); err != nil {
		return err
	}
	fmt.Println(src.Hash)
	return nil
}

// cmdAttach gives a URL source the bytes the app could not fetch: a page the human saved from their
// browser (HTML or PDF) because the site asked for a login or a subscription. The URL stays the
// address; the file becomes the snapshot.
func cmdAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	source := fs.String("source", "", "source hash (alternative to --url)")
	url := fs.String("url", "", "URL of the source")
	file := fs.String("file", "", "file to attach: .html, .pdf, .md or .txt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("attach requires --file")
	}
	if (*source == "") == (*url == "") {
		return errors.New("attach requires exactly one of --source or --url")
	}
	hash := strings.ToLower(strings.TrimSpace(*source))
	if *url != "" {
		hash = entities.URLHash(strings.TrimSpace(*url))
	}
	if !entities.IsSHA256Hex(hash) {
		return errors.New("attach needs a source hash or an absolute URL")
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	status, body, err := apiUpload(addr, "/sources/"+hash+"/attach", "file", filepath.Base(*file), data)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("attach failed: %s", string(body))
	}
	var src payloads.Source
	if err := json.Unmarshal(body, &src); err != nil {
		return err
	}
	fmt.Printf("%s · %s · %d bytes\n", src.Hash[:12], src.Title, src.Bytes)
	return nil
}

// cmdRefetch tries the URL again for an attached snapshot. A readable page replaces the attachment;
// a wall or a failure leaves it exactly as it was.
func cmdRefetch(args []string) error {
	fs := flag.NewFlagSet("refetch", flag.ContinueOnError)
	source := fs.String("source", "", "source hash (alternative to --url)")
	urlFlag := fs.String("url", "", "URL of the source")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*source == "") == (*urlFlag == "") {
		return errors.New("refetch requires exactly one of --source or --url")
	}
	hash := strings.ToLower(strings.TrimSpace(*source))
	if *urlFlag != "" {
		hash = entities.URLHash(strings.TrimSpace(*urlFlag))
	}
	if !entities.IsSHA256Hex(hash) {
		return errors.New("refetch needs a source hash or an absolute URL")
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	status, body, err := apiRequest(addr, http.MethodPost, "/sources/"+hash+"/refetch", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("refetch failed: %s", string(body))
	}
	var res payloads.RefetchResult
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}
	switch {
	case res.Replaced:
		fmt.Println("the page was readable — the attached snapshot was replaced")
	case res.Blocked:
		fmt.Println("still blocked — the attached snapshot was kept")
	case res.Failed:
		fmt.Println("the fetch failed — the attached snapshot was kept")
	default:
		fmt.Println("nothing to replace — the source is unchanged")
	}
	if res.Detail != "" {
		fmt.Println(res.Detail)
	}
	return nil
}

// apiUpload posts one file as multipart/form-data to the running instance.
func apiUpload(addr, path, field, filename string, data []byte) (int, []byte, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile(field, filename)
	if err != nil {
		return 0, nil, err
	}
	if _, err := part.Write(data); err != nil {
		return 0, nil, err
	}
	if err := mw.Close(); err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/api/v1"+path, &body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("no running instance at %s: %w", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, nil
}

func cmdSources(args []string) error {
	fs := flag.NewFlagSet("sources", flag.ContinueOnError)
	kind := fs.String("kind", "", "filter by kind: md, pdf, txt, url")
	limit := fs.Int("limit", 200, "max rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	query := fmt.Sprintf("/sources?limit=%d", *limit)
	if *kind != "" {
		query += "&kind=" + *kind
	}
	status, body, err := apiRequest(addr, http.MethodGet, query, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("sources failed: %s", string(body))
	}
	var list payloads.SourceList
	if err := json.Unmarshal(body, &list); err != nil {
		return err
	}
	for _, s := range list.Sources {
		title := s.Title
		if title == "" {
			title = "-"
		}
		fmt.Printf("%-4s %s  %-24s %8d  %s\n", s.Kind, s.Hash[:12], truncate(title, 24), s.Bytes, s.TreeState)
	}
	return nil
}

func cmdTree(args []string) error {
	fs := flag.NewFlagSet("tree", flag.ContinueOnError)
	source := fs.String("source", "", "source hash")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" {
		return errors.New("tree requires --source HASH")
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	status, body, err := apiRequest(addr, http.MethodGet, "/sources/"+*source+"/tree", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("tree failed: %s", string(body))
	}
	var t payloads.Tree
	if err := json.Unmarshal(body, &t); err != nil {
		return err
	}
	for _, n := range t.Nodes {
		title := n.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("%s%s %s\n", strings.Repeat("  ", n.Depth), n.NodeID, title)
	}
	return nil
}

// cmdReset throws a source's derivation away (tree, claims, pages) and starts it over.
func cmdReset(args []string) error {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	source := fs.String("source", "", "source hash to reset")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" {
		return errors.New("reset requires --source HASH")
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	status, body, err := apiRequest(addr, http.MethodPost, "/sources/"+*source+"/reset", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("reset failed: %s", string(body))
	}
	fmt.Println(strings.TrimSpace(string(body)))
	return nil
}

func cmdPost(args []string, path, name string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	status, body, err := apiRequest(addr, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("%s failed: %s", name, string(body))
	}
	fmt.Println(strings.TrimSpace(string(body)))
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// cmdModels lists the models the configured LLM endpoint serves, so you can pick
// model_compile / model_reason.
func cmdModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	config.LoadDotEnv(config.DotEnvCandidates(*cfgPath)...)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	timeout, _ := time.ParseDuration(cfg.OpenAI.Timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := gateways.ListModels(ctx, gateways.OpenAIConfig{
		BaseURL: cfg.OpenAI.BaseURL,
		APIKey:  cfg.OpenAI.APIKey,
		Timeout: timeout,
		Headers: cfg.OpenAI.Headers,
	})
	if err != nil {
		return err
	}
	for _, m := range models {
		fmt.Println(m)
	}
	return nil
}

func cmdBranches(args []string) error {
	fs := flag.NewFlagSet("branches", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	status, body, err := apiRequest(addr, http.MethodGet, "/topics/claims", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("branches failed: %s", string(body))
	}
	var list payloads.TopicUseList
	if err := json.Unmarshal(body, &list); err != nil {
		return err
	}
	for _, b := range list.Topics {
		fmt.Printf("%-30s %d\n", b.Topic, b.Count)
	}
	return nil
}

func cmdAtoms(args []string) error {
	fs := flag.NewFlagSet("atoms", flag.ContinueOnError)
	branch := fs.String("topic", "", "filter by topic")
	limit := fs.Int("limit", 200, "max rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	query := fmt.Sprintf("/atoms?limit=%d", *limit)
	if *branch != "" {
		query += "&topic=" + url.QueryEscape(*branch)
	}
	status, body, err := apiRequest(addr, http.MethodGet, query, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("atoms failed: %s", string(body))
	}
	var list payloads.AtomList
	if err := json.Unmarshal(body, &list); err != nil {
		return err
	}
	for _, a := range list.Atoms {
		fmt.Printf("%-24s %-12s %s\n", a.Topic, a.Type, a.Claim)
	}
	return nil
}

func cmdDistill(args []string) error {
	fs := flag.NewFlagSet("distill", flag.ContinueOnError)
	source := fs.String("source", "", "source hash to derive")
	reset := fs.Bool("reset", false, "replace the source's previous derivation instead of continuing it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" {
		return errors.New("distill requires --source HASH")
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	path := "/sources/" + *source + "/distill"
	status, body, err := apiRequest(addr, http.MethodPost, path, payloads.DistillRequest{Reset: *reset})
	if err != nil {
		return err
	}
	if status != http.StatusAccepted {
		return fmt.Errorf("distill failed: %s", string(body))
	}
	var res payloads.DistillResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}
	fmt.Println(res.RunID)
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	id := fs.String("id", "", "run id")
	asJSON := fs.Bool("json", false, "print raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("run requires --id ULID")
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	status, body, err := apiRequest(addr, http.MethodGet, "/runs/"+*id, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("run failed: %s", string(body))
	}
	if *asJSON {
		fmt.Println(string(body))
		return nil
	}
	var r payloads.Run
	if err := json.Unmarshal(body, &r); err != nil {
		return err
	}
	fmt.Printf("%s %s (%s)\n", r.Op, r.Status, r.Model)
	fmt.Printf("  target     %s\n", r.TargetID)
	fmt.Printf("  iterations %d, tokens %d (prompt %d / completion %d)\n", r.Iterations, r.TotalTokens, r.PromptTokens, r.CompletionTokens)
	if r.Error != "" {
		fmt.Printf("  error      %s\n", r.Error)
	}
	if r.Answer != "" {
		fmt.Printf("  answer     %s\n", r.Answer)
	}
	return nil
}

func cmdApp(args []string) error {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "config file path")
	headless := fs.Bool("headless", false, "run without the tray")
	noOpen := fs.Bool("no-open", false, "start the service and the tray, but do not open a browser")
	logLevel := fs.String("log-level", envOr("THOUGHT_KEEPER_LOG_LEVEL", "info"), "log level: debug|info|warn|error")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := logging.Setup(parseLevel(*logLevel))
	if loaded := config.LoadDotEnv(config.DotEnvCandidates(*cfgPath)...); len(loaded) > 0 {
		log.Info("loaded .env", "files", loaded)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	a, err := app.New(app.Options{
		Config: cfg, Path: *cfgPath, Log: log, Headless: *headless, NoOpen: *noOpen,
	})
	// Single instance: a second launch never starts a second engine — it hands the
	// vault lock over and simply opens the running instance's UI.
	if errors.Is(err, types.ErrLocked) {
		return handOverToRunning(log)
	}
	if err != nil {
		return err
	}
	return a.Run()
}

// handOverToRunning opens the UI of the instance that already holds the vault lock.
// A second engine is never started; the UI keeps itself single-window, so this does
// not pile up windows.
func handOverToRunning(log *slog.Logger) error {
	addr, err := app.ReadEndpoint()
	if err != nil {
		return fmt.Errorf("another instance is already running, but its address is unknown (%w); remove %s if it is stale", err, config.EndpointPath())
	}
	if err := app.Ping(addr); err != nil {
		return fmt.Errorf("another instance holds the vault lock but does not answer on %s (%w); remove %s if it is stale", addr, err, config.EndpointPath())
	}
	if err := app.OpenUI(addr); err != nil {
		log.Warn("could not open the running instance", "addr", addr, "err", err)
		fmt.Fprintf(os.Stderr, "Thought Keeper is already running: http://%s (could not open the UI: %v)\n", addr, err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "Thought Keeper is already running — opened http://%s\n", addr)
	log.Info("opened the running instance", "addr", addr)
	return nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// cmdOpen opens the running instance's UI via the OS handler.
func cmdOpen(args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	url := fs.String("url", "", "running instance address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	addr, err := resolveAddr(*url)
	if err != nil {
		return err
	}
	fmt.Println("http://" + addr)
	return app.OpenUIAddress(addr)
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	url := fs.String("url", "", "running instance address")
	asJSON := fs.Bool("json", false, "print the raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	addr, err := resolveAddr(*url)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/v1/status", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("no running instance at %s: %w", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status request failed: %s: %s", resp.Status, string(body))
	}
	if *asJSON {
		fmt.Println(string(body))
		return nil
	}
	var st payloads.Status
	if err := json.Unmarshal(body, &st); err != nil {
		return err
	}
	printStatus(st)
	return nil
}

func printStatus(s payloads.Status) {
	mark := func(ok bool) string {
		if ok {
			return "ok"
		}
		return "missing"
	}
	fmt.Printf("Thought Keeper %s\n", s.Version)
	fmt.Printf("  address    %s (pid %d, uptime %s)\n", s.App.Addr, s.App.PID, s.App.Uptime)
	fmt.Printf("  vault      %s (writable: %t)\n", s.Vault.Root, s.Vault.Writable)
	fmt.Printf("    sources  %s\n", s.Vault.Sources)
	fmt.Printf("    atoms    %s\n", s.Vault.Atoms)
	fmt.Printf("    clusters %s\n", s.Vault.Clusters)
	fmt.Printf("  index      %s (open: %t, search: %s)\n", s.Index.Path, s.Index.Open, s.Index.Search)
	if s.Index.Outdated != "" {
		// The one state in which the whole index is unusable: said plainly, with the file and the step.
		fmt.Printf("  !!         %s\n", s.Index.Outdated)
	}
	fmt.Printf("  counts     sources=%d atoms=%d clusters=%d unclustered=%d runs=%d\n",
		s.Index.Sources, s.Index.Atoms, s.Index.Clusters, s.Index.Unclustered, s.Index.Runs)
	fmt.Printf("  pdf        %s (%s)\n", mark(s.Tools.PDF), s.Tools.TextTool)
	fmt.Printf("  ocr        %s (%s + %s)\n", mark(s.Tools.OCR), s.Tools.Rasterizer, s.Tools.Tesseract)
	fmt.Printf("  agent      %s (%s)\n", mark(s.Agent.Configured), s.Agent.Model)
	peak := fmt.Sprintf("heap=%d MB peak=%d MB", s.Memory.HeapMB, s.Memory.PeakHeapMB)
	if s.Memory.PeakWork != "" {
		peak += " (" + s.Memory.PeakWork
		if s.Memory.PeakAt != "" {
			peak += " @ " + s.Memory.PeakAt
		}
		peak += ")"
	}
	fmt.Printf("  memory     %s gc=%d\n", peak, s.Memory.NumGC)
	fmt.Printf("  inbox      %s\n", inboxState(s.App.Inbox))
}

// inboxState says how the drop zone is noticed: on inotify, or by polling because the kernel refused a
// watch (ENOSPC from inotify is the per-user watch budget, never the disk).
func inboxState(in payloads.InboxWatch) string {
	if in.Watched {
		return "watching"
	}
	if in.Error == "" {
		return "unknown"
	}
	return "polling — " + in.Error
}

func resolveAddr(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if env := os.Getenv("THOUGHT_KEEPER_URL"); env != "" {
		return env, nil
	}
	if addr, err := app.ReadEndpoint(); err == nil {
		return addr, nil
	}
	return "", errors.New("no running instance found (is Thought Keeper running?)")
}

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	query := fs.String("query", "", "search query")
	layer := fs.String("layer", "", "atoms | sources (default: atoms)")
	limit := fs.Int("limit", 20, "max hits")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *query == "" {
		return errors.New("search requires --query")
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/search?q=%s&limit=%d", url.QueryEscape(*query), *limit)
	if *layer != "" {
		path += "&layer=" + *layer
	}
	code, body, err := apiRequest(addr, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("search failed: %s", string(body))
	}
	var res payloads.SearchResult
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}
	for _, h := range res.Hits {
		fmt.Printf("%-5s %s  %s\n      %s\n", h.Kind, h.ID, h.Title, h.Snippet)
	}
	return nil
}

func cmdQuery(args []string) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	question := fs.String("question", "", "question to ask the claims")
	wait := fs.Bool("wait", false, "wait for the answer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *question == "" {
		return errors.New("query requires --question")
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	code, body, err := apiRequest(addr, http.MethodPost, "/query", payloads.QueryRequest{Question: *question})
	if err != nil {
		return err
	}
	if code != http.StatusAccepted {
		return fmt.Errorf("query failed: %s", string(body))
	}
	var res payloads.DistillResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}
	if !*wait {
		fmt.Println(res.RunID)
		return nil
	}
	run, err := waitRun(addr, res.RunID)
	if err != nil {
		return err
	}
	if run.Status != "succeeded" {
		return fmt.Errorf("query %s: %s", run.Status, run.Error)
	}
	fmt.Println(run.Answer)
	return nil
}

func cmdLint(args []string) error {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the lint pass")
	kind := fs.String("kind", "", "filter findings by kind")
	if err := fs.Parse(args); err != nil {
		return err
	}
	addr, err := resolveAddr("")
	if err != nil {
		return err
	}
	code, body, err := apiRequest(addr, http.MethodPost, "/lint", nil)
	if err != nil {
		return err
	}
	if code != http.StatusAccepted {
		return fmt.Errorf("lint failed: %s", string(body))
	}
	var res payloads.DistillResponse
	_ = json.Unmarshal(body, &res)
	if *wait {
		if _, err := waitRun(addr, res.RunID); err != nil {
			return err
		}
	}
	path := "/lint/findings"
	if *kind != "" {
		path += "?kind=" + url.QueryEscape(*kind)
	}
	fc, fb, err := apiRequest(addr, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if fc != http.StatusOK {
		return fmt.Errorf("findings failed: %s", string(fb))
	}
	var list payloads.FindingList
	if err := json.Unmarshal(fb, &list); err != nil {
		return err
	}
	for _, f := range list.Findings {
		fmt.Printf("%-8s %-16s %s\n", f.Severity, f.Kind, f.Message)
	}
	return nil
}

func waitRun(addr, id string) (payloads.Run, error) {
	for range 300 {
		time.Sleep(2 * time.Second)
		code, body, err := apiRequest(addr, http.MethodGet, "/runs/"+id, nil)
		if err != nil {
			return payloads.Run{}, err
		}
		if code != http.StatusOK {
			return payloads.Run{}, fmt.Errorf("run failed: %s", string(body))
		}
		var run payloads.Run
		if err := json.Unmarshal(body, &run); err != nil {
			return payloads.Run{}, err
		}
		if run.Status != "running" {
			return run, nil
		}
	}
	return payloads.Run{}, errors.New("timed out waiting for the run")
}
