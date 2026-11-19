package usecases

import (
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// windowDeferral reports whether err is the derivation's model window refusing or cutting a call,
// and the reason a run records for it. A deferral is not a failure: the work is kept and resumes when
// the window closes, so the run row says `aborted` with the reopening instant instead of `failed`.
func windowDeferral(err error) (bool, string) {
	reopen, ok := interfaces.WindowReopen(err)
	if !ok {
		return false, ""
	}
	return true, "deferred: the model window is closed until " + reopen.UTC().Format(time.RFC3339)
}
