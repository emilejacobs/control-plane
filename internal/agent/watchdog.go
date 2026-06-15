package agent

import (
	"context"
	"io"
	"log/slog"
	"time"
)

// watchdog detects a wedged MQTT session and forces recovery (#65). Paho's
// built-in auto-reconnect is already enabled but proved insufficient: a session
// can enter a state where paho believes it is disconnected (publishes fail with
// "not Connected") yet never re-establishes, leaving a device stuck for hours.
//
// The watchdog watches liveness — the time of the last *successful* publish.
// The agent heartbeats every 30s, so once that timestamp falls more than
// staleAfter behind, the session is dead rather than merely between beats. The
// recovery action (onWedged) is to exit the process: launchd's KeepAlive
// restarts it through the resident supervisor with a fresh transport, which is
// exactly the proven manual fix (`launchctl kickstart -k`). Process exit is
// simpler and lower-risk than an in-process paho teardown — and paho's own
// reconnect already failed in the observed wedge.
type watchdog struct {
	// lastSuccess reports when a publish last succeeded (transport-owned).
	lastSuccess func() time.Time
	// now defaults to time.Now; injectable for tests.
	now func() time.Time
	// staleAfter is how far behind lastSuccess may fall before the session is
	// declared wedged. checkInterval is how often that is evaluated.
	staleAfter    time.Duration
	checkInterval time.Duration
	// onWedged is invoked once when a wedge is confirmed; run then returns.
	onWedged func()
	logger   *slog.Logger
}

// run blocks until the context is cancelled or a wedge is confirmed. On a
// confirmed wedge it logs, fires onWedged once, and returns — recovery (process
// exit) is the caller's responsibility, so there is no in-process reconnect
// storm to back off from.
func (w *watchdog) run(ctx context.Context) {
	log := w.logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	nowFn := w.now
	if nowFn == nil {
		nowFn = time.Now
	}

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stale := nowFn().Sub(w.lastSuccess())
			if stale > w.staleAfter {
				log.Error("mqtt session wedged: no successful publish within window — forcing recovery",
					"stale_for", stale.String(),
					"stale_after", w.staleAfter.String(),
				)
				w.onWedged()
				return
			}
		}
	}
}
