package nildatest

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// UseEmailTemplates loads the mail your plugin declares (plugin.json "email_templates") so SendTemplatedEmail renders
// their defaults, as Core renders the owner's words: a test then asserts the real subject and text. A key not loaded
// is refused with NotFound, as Core refuses a template the plugin never declared.
//
//	host.UseEmailTemplates(nildatest.ManifestEmailTemplates(t, "plugin.json"))
func (h *Host) UseEmailTemplates(decls []nilda.EmailTemplateDecl) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.templates = make(map[string]nilda.EmailTemplateDecl, len(decls))
	for _, d := range decls {
		h.templates[d.Key] = d
	}
}

// SwitchOffTemplate is the owner switching one of the plugin's mails off: SendTemplatedEmail answers
// nilda.ErrTemplateOff for it and records nothing.
func (h *Host) SwitchOffTemplate(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.off == nil {
		h.off = map[string]bool{}
	}
	h.off[key] = true
}

// ManifestEmailTemplates reads the "email_templates" a plugin.json declares.
func ManifestEmailTemplates(t testing.TB, manifestPath string) []nilda.EmailTemplateDecl {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	var m struct {
		EmailTemplates []nilda.EmailTemplateDecl `json:"email_templates"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", manifestPath, err)
	}
	return m.EmailTemplates
}

// bestTemplateLocale is the language Core picks: the one asked, then its primary language, then English.
func bestTemplateLocale(defaults map[string]nilda.EmailTemplateText, want string) string {
	want = strings.ToLower(strings.TrimSpace(want))
	if _, ok := defaults[want]; ok && want != "" {
		return want
	}
	if primary, _, _ := strings.Cut(want, "-"); primary != "" {
		if _, ok := defaults[primary]; ok {
			return primary
		}
	}
	return "en"
}

// splitTemplateData gives a variable of kind html its two halves, as Core does.
func splitTemplateData(decl []nilda.EmailVariable, data map[string]any) (htmlVars, textVars map[string]any) {
	htmlVars, textVars = map[string]any{}, map[string]any{}
	for k, v := range data {
		htmlVars[k], textVars[k] = v, v
	}
	for _, v := range decl {
		if v.Kind != nilda.EmailHTML {
			continue
		}
		if m, ok := data[v.Name].(map[string]any); ok {
			markup, _ := m["html"].(string)
			plain, _ := m["text"].(string)
			htmlVars[v.Name], textVars[v.Name] = safeHTML(markup), plain
		}
	}
	return htmlVars, textVars
}

var (
	emptyParagraph = regexp.MustCompile(`<p(\s[^>]*)?>\s*</p>`)
	blankRun       = regexp.MustCompile(`\n[ \t]*(\n[ \t]*)+\n`)
)

// tidyText is Core's: trimmed, every run of blank lines one.
func tidyText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(blankRun.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
