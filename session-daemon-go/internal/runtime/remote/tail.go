package remote

import (
	"context"
	"io"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/activity"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
)

const (
	tailBackoffMin = 2 * time.Second
	tailBackoffMax = 2 * time.Minute
	// A stream that stayed up this long counts as healthy and resets the backoff.
	tailHealthy = 30 * time.Second
	// Remote hosts keep the same spool layout as the local one.
	remoteCodexSpool = "$HOME/.codex/astrorder-observer/events"
)

// TailStarter opens a remote command; *ssh.Transport.Start satisfies it.
type TailStarter func(ctx context.Context, spec sshtrans.CommandSpec) (TailSession, error)

// TailSession is the slice of *ssh.Session the supervisor needs.
type TailSession interface {
	Stdout() io.ReadCloser
	Wait() (sshtrans.ExitStatus, error)
	Close() error
}

// tailScript finds the installed helper without a login shell and streams the
// spool. The newest hash-installed helper wins; PATH is the fallback. It exits
// non-zero when no helper can `tail`, so the supervisor backs off instead of
// spinning against an old helper.
const tailScript = `set -u
spool="` + remoteCodexSpool + `"
helper=""
for candidate in $(ls -t "$HOME"/.local/lib/astrorder/helper/*/astrorder-helper 2>/dev/null); do
  if [ -x "$candidate" ]; then helper="$candidate"; break; fi
done
if [ -z "$helper" ]; then helper=$(command -v astrorder-helper 2>/dev/null || true); fi
[ -n "$helper" ] || { echo "astrorder-helper is not installed" >&2; exit 127; }
mkdir -p -- "$spool"
exec "$helper" tail --spool "$spool"`

// SuperviseTail keeps one `astrorder-helper tail` stream open for a remote host
// and feeds it to activity.Consume until ctx ends. It reconnects with capped
// exponential backoff; events missed while disconnected are repaired by the
// App's manual or start-up reconciliation, never by polling.
func SuperviseTail(ctx context.Context, start TailStarter, opts activity.Options, sleep func(context.Context, time.Duration)) {
	if sleep == nil {
		sleep = sleepContext
	}
	backoff := tailBackoffMin
	for ctx.Err() == nil {
		began := time.Now()
		runTailOnce(ctx, start, opts)
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) >= tailHealthy {
			backoff = tailBackoffMin
		}
		sleep(ctx, backoff)
		if backoff *= 2; backoff > tailBackoffMax {
			backoff = tailBackoffMax
		}
	}
}

func runTailOnce(ctx context.Context, start TailStarter, opts activity.Options) {
	session, err := start(ctx, sshtrans.CommandSpec{Executable: "sh", Args: []string{"-c", tailScript}})
	if err != nil {
		return
	}
	defer session.Close()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-done:
		}
	}()
	_ = activity.Consume(session.Stdout(), opts)
	close(done)
	_, _ = session.Wait()
}

func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
