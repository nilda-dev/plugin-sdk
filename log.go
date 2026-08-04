package nilda

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// LOGGING FROM A PLUGIN.
//
// A plugin is a separate PROCESS. `fmt.Println` from one lands wherever the host happened to leave stdout
// pointing, which in production is nowhere anybody looks — and stdout is not free to write to at all: it
// carries the go-plugin handshake, so scribbling on it breaks the connection.
//
// The mechanism existed on both ends with no door in the middle. go-plugin reads every line a plugin writes
// to STDERR and runs it through `parseJSON` (go-plugin/log_entry.go): a line shaped like hclog's JSON is
// re-emitted through the HOST's logger at the level it declares, and anything else is dumped at Debug as an
// opaque string. Core builds that host logger already — named `plugin.<key>`, at the site's configured
// level (`internal/plugin/host.go`).
//
// So the plumbing was complete except that no SDK author had a logger that speaks the format. This is that
// logger, and the format is not negotiable — the three `@`-prefixed keys below are what go-plugin reads.
//
// # The author writes slog, not hclog
//
// slog is the standard library's logger and what Core itself uses everywhere. hclog is the WIRE FORMAT
// between two processes, not something a plugin author should have to learn, so it stays an implementation
// detail of this file. That also means the SDK does not force hclog into a plugin's dependency graph for
// the sake of one log line.
//
// # Not a capability
//
// It needs no grant. A plugin writing to its own stderr reaches nothing of the site's, and the alternative
// is not a quieter plugin — it is an unreadable one, whose author cannot leave a trace on a site they will
// never have access to.

// Log returns the plugin's logger.
//
// Safe before Init and safe on a nil Core: a plugin that fails during startup is exactly when its author
// most needs to have written something down.
func Log() *slog.Logger { return defaultLogger() }

// Log is the same logger reached from the Core handle a plugin already holds, so a handler with `core` in
// hand needs no second name in its head. Named after the plugin once Core has said what its key is.
func (c *Core) Log() *slog.Logger {
	if c == nil || c.logger == nil {
		return defaultLogger()
	}
	return c.logger
}

var (
	defaultOnce sync.Once
	defaultLog  *slog.Logger
)

func defaultLogger() *slog.Logger {
	defaultOnce.Do(func() { defaultLog = newLogger(os.Stderr, "") })
	return defaultLog
}

// newLogger builds a logger whose records reach Core.
//
// `name` is the plugin key, added as a field so a line is attributable even if the host's own naming ever
// changes; empty before Init has said what the key is.
func newLogger(w io.Writer, name string) *slog.Logger {
	return slog.New(&hostHandler{w: w, name: name})
}

// hostHandler writes each record as one JSON line in the shape go-plugin parses.
//
// Level is NOT filtered here, deliberately. Core applies the site's configured level when it re-emits, and
// filtering in both places means an operator who turns the level up to debug a misbehaving plugin still
// sees nothing — the plugin having silently dropped the line first.
type hostHandler struct {
	w     io.Writer
	name  string
	attrs []slog.Attr
	group string
	mu    sync.Mutex
}

func (h *hostHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *hostHandler) Handle(_ context.Context, r slog.Record) error {
	// The three @-prefixed keys are go-plugin's contract; everything else on the line becomes a KV pair on
	// the host's log entry.
	out := map[string]any{
		"@message": r.Message,
		"@level":   levelName(r.Level),
		// The exact layout go-plugin's time.Parse expects. A timestamp it cannot parse makes it reject the
		// WHOLE line as non-JSON, so the message would arrive as an opaque debug string.
		"@timestamp": r.Time.Format("2006-01-02T15:04:05.000000Z07:00"),
	}
	if h.name != "" {
		out["plugin"] = h.name
	}
	for _, a := range h.attrs {
		putAttr(out, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		putAttr(out, h.group, a)
		return true
	})
	line, err := json.Marshal(out)
	if err != nil {
		// A value that will not marshal must not silence the message. Report the message alone rather than
		// dropping the record, because the attribute was the optional half.
		line, err = json.Marshal(map[string]any{
			"@message": r.Message, "@level": levelName(r.Level),
			"@timestamp": r.Time.Format("2006-01-02T15:04:05.000000Z07:00"),
			"logerror":   err.Error(),
		})
		if err != nil {
			return err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, werr := h.w.Write(append(line, '\n'))
	return werr
}

func (h *hostHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &hostHandler{w: h.w, name: h.name, group: h.group}
	next.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return next
}

func (h *hostHandler) WithGroup(name string) slog.Handler {
	next := &hostHandler{w: h.w, name: h.name, group: h.group}
	next.attrs = append([]slog.Attr(nil), h.attrs...)
	if name != "" {
		if next.group != "" {
			next.group += "." + name
		} else {
			next.group = name
		}
	}
	return next
}

// putAttr flattens one attribute onto the line. Groups become dotted keys, because go-plugin's KV pairs are
// flat — a nested object would render as an unreadable Go map in the operator's log.
func putAttr(out map[string]any, group string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	key := a.Key
	if key == "" {
		return
	}
	if group != "" {
		key = group + "." + key
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, sub := range a.Value.Group() {
			putAttr(out, key, sub)
		}
		return
	}
	// The @-prefixed names are go-plugin's; an attribute must never be able to forge the level or the
	// message of the line carrying it.
	if key == "@message" || key == "@level" || key == "@timestamp" {
		key = "attr." + key
	}
	switch v := a.Value.Any().(type) {
	case error:
		out[key] = v.Error()
	case time.Time:
		out[key] = v.Format(time.RFC3339Nano)
	default:
		out[key] = a.Value.Any()
	}
}

// levelName maps slog levels onto hclog's names, which is the vocabulary go-plugin re-emits at.
func levelName(l slog.Level) string {
	switch {
	case l < slog.LevelDebug:
		return "trace"
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}
