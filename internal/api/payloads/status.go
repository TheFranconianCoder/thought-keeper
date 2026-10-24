// Package payloads defines the JSON wire shapes and the single error mapping.
package payloads

// Status is the doctor report returned by GET /api/v1/status. It is a diagnostic
// surface, not a liveness probe.
type Status struct {
	Version string    `json:"version"`
	App     AppStatus `json:"app"`
	Vault   VaultInfo `json:"vault"`
	Index   IndexInfo `json:"index"`
	Tools   ToolsInfo `json:"tools"`
	Agent   AgentInfo `json:"agent"`
	Runs    RunsInfo  `json:"runs"`
	// Memory is the heap account of this process: the association step spikes it and frees it again,
	// so the peak is the number that answers "how much did a run cost".
	Memory Memory `json:"memory"`
	// Pipeline is the global step status (also served on its own at GET /api/v1/pipeline).
	Pipeline Pipeline `json:"pipeline"`
	// Summary is the source-overview backfill: how many summaries the last consistency walk found
	// missing, and how many are queued or running. Nil when there is nothing to do.
	Summary *SummaryStatus `json:"summary,omitempty"`
	Message string         `json:"message,omitempty"`
}

// SummaryStatus is the source-overview backfill as the reader sees it while it runs. "Missing" is the
// backlog the consistency job measured (a summary is owed by every completely distilled source that
// has no <hash>.summary.md); "queued" and "running" are the summary worker's live state.
type SummaryStatus struct {
	Missing int `json:"missing"`
	Queued  int `json:"queued"`
	Running int `json:"running"`
	// At is when the backlog was last measured (RFC3339).
	At string `json:"at,omitempty"`
}

// Pipeline is the GLOBAL status: which of the four strictly separated steps is running right now.
// The four steps are capture → outline → distill → cluster; the last one runs over the whole vault
// and only while no document step runs, which is why "documents" and "clusters" are the two working
// states and never both at once.
type Pipeline struct {
	// Active is "idle", "documents" or "clusters".
	Active string `json:"active"`
	// Running and Queued count per step (capture, outline, distill, cluster).
	Running map[string]int `json:"running"`
	Queued  map[string]int `json:"queued"`
	// Asking is how many queries are in flight. Ask is never gated.
	Asking int `json:"asking"`
	// Jobs are the pieces of work behind those counts, one entry each. What a run REPORTS while it
	// works (its phase, its parts) travels with the source's progress; this is what the app holds.
	Jobs []Job `json:"jobs,omitempty"`
}

// Job is one piece of work the app holds RIGHT NOW: which of the steps it is, what it is about, and
// whether it is executing or still waiting.
//
// The counts say how busy the app is; this says what it is busy WITH. "Is it hung or just slow?" and
// "which source is being processed?" are the questions a reader actually has, and a number cannot
// answer either of them.
type Job struct {
	// Step is capture | outline | distill | cluster | query | lint.
	Step string `json:"step"`
	// State is running (it is EXECUTING), queued (it waits in a queue) or waiting (a worker took it,
	// but the gate is not free: the cluster pass holds it). Only `running` means the work is
	// happening — a run that waits for the cluster step must not look like it runs beside it, because
	// the four steps forbid exactly that.
	State string `json:"state"`
	// Why names the reason a job is not running, when there is one.
	Why string `json:"why,omitempty"`
	// Target is the source hash, empty for a step that runs over the whole vault.
	Target string `json:"target,omitempty"`
	// Title is the source's title, so a row reads like something instead of like a hash. The reader
	// falls back to the hash when it is empty.
	Title string `json:"title,omitempty"`
	// Detail is what a maintenance job is doing RIGHT NOW ("branches · asking the model", "applying 12
	// merges"). A pass that takes a minute without saying what it is doing is a pass nobody can judge.
	Detail string `json:"detail,omitempty"`
	// Since is when the app took the work on (RFC 3339): a run that has been claimed for longer than a
	// whole run budget is the one that is in trouble.
	Since string `json:"since,omitempty"`
}

// Memory is the runtime's own account of the heap: heap_mb is what is live now, heap_peak_mb the
// highest live value the app has observed (sampled every 2 s — a pass lasts far longer than the
// gap), sys_mb what the runtime holds from the OS in total, num_gc how many collections ran.
// The association step is the peak of the app: it reads every claim.
//
// peak_work and peak_at NAME the watermark — which pass was running when the heap was highest, and
// when. A peak without an attribution starts an argument instead of ending one.
type Memory struct {
	HeapMB     int64  `json:"heap_mb"`
	PeakHeapMB int64  `json:"heap_peak_mb"`
	SysMB      int64  `json:"sys_mb"`
	NumGC      uint32 `json:"num_gc"`
	PeakWork   string `json:"peak_work,omitempty"`
	PeakAt     string `json:"peak_at,omitempty"`
}

// RunsInfo is the live state of the run workers: how many are queued and how many sources are
// being worked on right now. Without it, "is it hung or just slow?" needs guesswork — a run stuck
// against a slow endpoint looks exactly like a run that never started.
type RunsInfo struct {
	PipelineQueued  int `json:"pipeline_queued"`
	PipelineRunning int `json:"pipeline_running"`
	PipelineWorkers int `json:"pipeline_workers"`
	QueryQueued     int `json:"query_queued"`
	LintQueued      int `json:"lint_queued"`
	LintRunning     int `json:"lint_running"`
}

// AgentInfo reports whether the agent is configured.
type AgentInfo struct {
	Configured bool   `json:"configured"`
	Model      string `json:"model"`
	BaseURL    string `json:"base_url,omitempty"`
}

// InboxWatch is how the inbox is being noticed: on inotify, or by polling when the kernel refuses a
// watch (ENOSPC there is the inotify budget, not the disk).
type InboxWatch struct {
	Watched bool   `json:"watched"`
	Error   string `json:"error,omitempty"`
}

type AppStatus struct {
	Addr     string `json:"addr"`
	PID      int    `json:"pid"`
	Headless bool   `json:"headless"`
	// Inbox is the drop zone's state: watched (inotify) or only polled, and why.
	Inbox     InboxWatch `json:"inbox"`
	StartedAt string     `json:"started_at"`
	Uptime    string     `json:"uptime"`
}

type VaultInfo struct {
	Root     string `json:"root"`
	Sources  string `json:"sources"`
	Inbox    string `json:"inbox"`
	Atoms    string `json:"atoms"`
	Clusters string `json:"clusters"`
	Writable bool   `json:"writable"`
}

type IndexInfo struct {
	Path string `json:"path"`
	Open bool   `json:"open"`
	// Outdated is the sentence for an index written by a build with ANOTHER SHAPE: empty when the index
	// is the one this build writes (or when it is new). The app reports it and touches nothing — the
	// index lives in the vault, and deleting it is the user's decision. Every read and write fails while
	// it is set, so it is said where the user looks: the status report, the start-up log and the CLI.
	Outdated string `json:"outdated,omitempty"`
	FTS5     bool   `json:"fts5"`
	Search   string `json:"search"` // "fts5" | "degraded"
	Sources  int64  `json:"sources"`
	// Bookmarks, Failed and Findings are the badges the sidebar shows. They are VAULT-WIDE counts,
	// not counts of whatever page of the source list is loaded: a badge that grew page by page read
	// as the app changing its mind.
	Bookmarks int64 `json:"bookmarks"`
	Failed    int64 `json:"failed"`
	Findings  int64 `json:"findings"`
	Atoms     int64 `json:"atoms"`
	// Clusters is the association layer; Unclustered is the claims it does not own yet (the backlog
	// of step 4).
	Clusters    int64 `json:"clusters"`
	Unclustered int64 `json:"unclustered"`
	Runs        int64 `json:"runs"`
}

type ToolsInfo struct {
	PDF        bool   `json:"pdf"`
	OCR        bool   `json:"ocr"`
	TextTool   string `json:"text_tool"`
	Rasterizer string `json:"rasterizer"`
	Tesseract  string `json:"tesseract"`
}
