package nilda

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// The whole value of this logger is that go-plugin PARSES the line. A logger whose output it cannot read
// still "works" — the plugin author sees nothing wrong, and every message arrives in the operator's log as
// an opaque debug string with no level and no fields. So these tests assert the wire shape, not that
// something was written.

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("a log line is not JSON, so go-plugin will dump it as an opaque string: %q (%v)", l, err)
		}
		out = append(out, m)
	}
	return out
}

// TestALineIsShapedTheWayGoPluginReadsIt — the three @-prefixed keys are go-plugin's contract
// (go-plugin/log_entry.go parseJSON). Miss one and the level is lost; malform the timestamp and the WHOLE
// line is rejected as non-JSON.
func TestALineIsShapedTheWayGoPluginReadsIt(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, "my_seo").Info("indexed a page", "url", "/about", "took_ms", 12)

	got := lines(t, &buf)
	if len(got) != 1 {
		t.Fatalf("want one line, got %d", len(got))
	}
	l := got[0]
	if l["@message"] != "indexed a page" {
		t.Errorf("@message = %v", l["@message"])
	}
	if l["@level"] != "info" {
		t.Errorf("@level = %v, want info", l["@level"])
	}
	// go-plugin parses this with an exact layout and REJECTS the whole entry on failure.
	ts, ok := l["@timestamp"].(string)
	if !ok {
		t.Fatal("@timestamp missing — go-plugin would reject the line")
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000000Z07:00", ts); err != nil {
		t.Errorf("@timestamp %q is not in the layout go-plugin parses: %v", ts, err)
	}
	// Everything else becomes a KV pair on the host's entry.
	if l["url"] != "/about" {
		t.Errorf("attribute lost: %v", l)
	}
	if l["plugin"] != "my_seo" {
		t.Errorf("the line must say which plugin wrote it: %v", l["plugin"])
	}
}

// TestEveryLevelMapsOntoOneGoPluginRepeats — hclog's vocabulary, because that is what the host re-emits at.
// A level it does not recognise silently becomes the default, so the operator's filter stops working.
func TestEveryLevelMapsOntoOneGoPluginRepeats(t *testing.T) {
	for _, c := range []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug - 4, "trace"},
		{slog.LevelDebug, "debug"},
		{slog.LevelInfo, "info"},
		{slog.LevelWarn, "warn"},
		{slog.LevelError, "error"},
		{slog.LevelError + 4, "error"},
	} {
		var buf bytes.Buffer
		newLogger(&buf, "p").Log(t.Context(), c.level, "m")
		if got := lines(t, &buf)[0]["@level"]; got != c.want {
			t.Errorf("level %v → %q, want %q", c.level, got, c.want)
		}
	}
}

// TestNothingIsFilteredInThePlugin — Core applies the site's configured level when it re-emits. Filtering
// here as well means an operator who turns the level up to debug a misbehaving plugin still sees nothing,
// because the plugin dropped the line before it ever left the process.
func TestNothingIsFilteredInThePlugin(t *testing.T) {
	var buf bytes.Buffer
	log := newLogger(&buf, "p")
	log.Debug("a debug line")
	log.Log(t.Context(), slog.LevelDebug-4, "a trace line")
	if n := len(lines(t, &buf)); n != 2 {
		t.Fatalf("both lines must leave the process; got %d", n)
	}
}

// TestAnAttributeCannotForgeTheLevel — the @-names are go-plugin's. A plugin logging an attribute called
// "@level" would otherwise rewrite the severity of the line carrying it, which is a way to make an error
// arrive as a trace nobody reads.
func TestAnAttributeCannotForgeTheLevel(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, "p").Error("real failure", "@level", "trace", "@message", "nothing to see")

	l := lines(t, &buf)[0]
	if l["@level"] != "error" {
		t.Errorf("an attribute overwrote the level: %v", l["@level"])
	}
	if l["@message"] != "real failure" {
		t.Errorf("an attribute overwrote the message: %v", l["@message"])
	}
	if l["attr.@level"] != "trace" {
		t.Errorf("the attribute itself should survive, renamed: %v", l)
	}
}

// TestGroupsFlatten — go-plugin's KV pairs are flat, so a nested object renders in the operator's log as an
// unreadable Go map.
func TestGroupsFlatten(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, "p").WithGroup("http").With("method", "GET").Info("served", "status", 200)

	l := lines(t, &buf)[0]
	if l["http.method"] != "GET" || l["http.status"] != float64(200) {
		t.Errorf("group keys did not flatten: %v", l)
	}
}

// TestAnErrorIsReadable — slog stores an error as an opaque value; JSON-marshalling one usually yields
// `{}`, so the single most common thing anybody logs would arrive empty.
func TestAnErrorIsReadable(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, "p").Error("upload failed", "error", errors.New("connection refused"))

	if got := lines(t, &buf)[0]["error"]; got != "connection refused" {
		t.Errorf("error = %v, want its message", got)
	}
}

// TestAnUnmarshalableAttributeDoesNotSilenceTheMessage — the attribute is the optional half. Dropping the
// record because one field would not marshal loses the thing the author was trying to say.
func TestAnUnmarshalableAttributeDoesNotSilenceTheMessage(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, "p").Error("the important part", "bad", make(chan int))

	l := lines(t, &buf)[0]
	if l["@message"] != "the important part" {
		t.Fatalf("the message was lost: %v", l)
	}
	if l["logerror"] == nil {
		t.Errorf("the failure to marshal should be visible, not silent: %v", l)
	}
}

// TestConcurrentWritersDoNotInterleave — a plugin serves HTTP and hooks at once, so two goroutines log at
// the same time. A torn line is not JSON, and go-plugin drops the whole entry to an opaque string.
func TestConcurrentWritersDoNotInterleave(t *testing.T) {
	var buf bytes.Buffer
	log := newLogger(&buf, "p")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			log.Info("concurrent", "i", i)
		}(i)
	}
	wg.Wait()
	if n := len(lines(t, &buf)); n != 50 {
		t.Fatalf("want 50 whole lines, got %d", n)
	}
}

// TestLogIsUsableBeforeInit — a plugin that fails during startup is exactly when its author most needs to
// have written something down, and Core has not yet told it its key.
func TestLogIsUsableBeforeInit(t *testing.T) {
	if Log() == nil {
		t.Fatal("the package logger must exist before Init")
	}
	var nilCore *Core
	if nilCore.Log() == nil {
		t.Fatal("Core.Log must be nil-safe")
	}
	Log().Info("this must not panic")
}

// TestDerivedLoggersDoNotShredEachOthersLines.
//
// `With(...)` returns a new handler over the SAME writer. With a mutex by value each derived logger got its
// own, so two goroutines could interleave bytes on one file descriptor — and go-plugin parses a plugin's
// stderr LINE BY LINE, so a torn line is not cosmetic: the host cannot parse it, re-emits it as an opaque
// debug string, and the record is lost.
//
// TWO assertions, because only one of them can actually fail on demand.
//
// The concurrency half below checks that sixteen goroutines holding sixteen DIFFERENT derived loggers
// produce sixteen well-formed lines. It is a real end-to-end check and it is NOT proof of the mutex: a
// torn write needs a real pipe and a line over PIPE_BUF, and even then it is a race that may not happen —
// a test that only sometimes fails is worse than none. Reintroducing the bug leaves this half green.
//
// So the invariant is asserted DIRECTLY, in the sibling test below: a derived handler must share its
// parent's mutex. That is deterministic, it is the actual property, and it fails the moment somebody
// writes `mu: &sync.Mutex{}` in WithAttrs again.
func TestDerivedLoggersDoNotShredEachOthersLines(t *testing.T) {
	var buf lockedBuffer
	base := newLogger(&buf, "acme")
	long := strings.Repeat("x", 8192)

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Every goroutine holds a DIFFERENT derived logger, which is the shape the bug had.
			base.With("worker", n).With("stage", "run").Info("a long line", "payload", long)
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 16 {
		t.Fatalf("wrote %d lines for 16 records — they interleaved", len(lines))
	}
	for i, l := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("line %d is not parseable JSON, so the host would drop it: %v", i, err)
		}
		if rec["@message"] != "a long line" || rec["plugin"] != "acme" {
			t.Errorf("line %d lost its fields: %v", i, rec["@message"])
		}
		if s, _ := rec["payload"].(string); len(s) != len(long) {
			t.Errorf("line %d's payload is %d bytes, want %d — it was truncated by an interleave", i, len(s), len(long))
		}
	}
}

// lockedBuffer stands in for the stderr pipe: it records what was written without adding synchronisation
// the handler is supposed to provide, so an unsynchronised handler shows up as garbled content rather than
// as a race on the buffer itself.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestADerivedHandlerSharesItsParentsMutex is the deterministic half of the pair above: `With(...)` builds
// a handler over the SAME writer, so it must not bring its own lock. This is what actually fails if the
// sharing is undone.
func TestADerivedHandlerSharesItsParentsMutex(t *testing.T) {
	base := &hostHandler{w: io.Discard, name: "acme", mu: &sync.Mutex{}}

	withAttrs, ok := base.WithAttrs([]slog.Attr{slog.String("a", "1")}).(*hostHandler)
	if !ok {
		t.Fatal("WithAttrs must return a *hostHandler")
	}
	if withAttrs.mu != base.mu {
		t.Error("WithAttrs gave the derived handler its own mutex — two loggers over one writer, each " +
			"locking something the other does not")
	}

	withGroup, ok := base.WithGroup("http").(*hostHandler)
	if !ok {
		t.Fatal("WithGroup must return a *hostHandler")
	}
	if withGroup.mu != base.mu {
		t.Error("WithGroup gave the derived handler its own mutex")
	}

	// And a chain of them, which is what an author actually writes.
	chained := base.WithAttrs([]slog.Attr{slog.Int("n", 1)}).WithGroup("db").WithAttrs([]slog.Attr{slog.Bool("ok", true)})
	if chained.(*hostHandler).mu != base.mu {
		t.Error("a chain of derivations lost the shared mutex")
	}

	// newLogger must supply one at all — a nil mutex is a panic on the first line written.
	fresh := newLogger(io.Discard, "acme")
	fresh.Info("this must not panic")
}

// M12 (2026-09-23 plugin hunt): slog's grouping rules, which the handler broke twice.
//
// A group qualifies what is added AFTER it. `With("order", …).WithGroup("payment")` filed the order id under
// payment.order, because stored attributes took whatever group was open when the line was written.
// And a group with an empty key is inlined — slog.Group("", …) and a LogValuer returning a group rely on it —
// where the handler dropped it and every attribute inside.
func TestGroupsFollowSlogsRules(t *testing.T) {
	var buf bytes.Buffer
	log := newLogger(&buf, "shop")
	log.With("order", "o-1").WithGroup("payment").With("gateway", "stripe").Info("charged", "amount", 500)
	log.Info("inline", slog.Group("", slog.String("sku", "A1")))
	log.WithGroup("cart").Info("inline in a group", slog.Group("", slog.Int("items", 3)))
	log.Info("empty key ignored", slog.String("", "dropped"), "kept", true)

	got := lines(t, &buf)
	if len(got) != 4 {
		t.Fatalf("want 4 lines, got %d", len(got))
	}
	for k, want := range map[string]any{"order": "o-1", "payment.gateway": "stripe", "payment.amount": float64(500)} {
		if got[0][k] != want {
			t.Errorf("line 1: %s = %v, want %v (line: %v)", k, got[0][k], want, got[0])
		}
	}
	if _, wrong := got[0]["payment.order"]; wrong {
		t.Error("an attribute added before WithGroup was filed inside the group")
	}
	if got[1]["sku"] != "A1" {
		t.Errorf("an empty-keyed group was not inlined: %v", got[1])
	}
	if got[2]["cart.items"] != float64(3) {
		t.Errorf("an empty-keyed group inside an open group was not inlined there: %v", got[2])
	}
	if got[3]["kept"] != true || got[3][""] != nil {
		t.Errorf("an empty-keyed plain attribute must be ignored, the rest kept: %v", got[3])
	}
}
