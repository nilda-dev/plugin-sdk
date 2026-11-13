package nilda

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The unit a bound counts in, as Core counts it.
type boundUnit int

const (
	unitCount boundUnit = iota // entries of a list or a map
	unitRunes                  // utf8.RuneCountInString, or len([]rune(…))
	unitBytes                  // len of a string or of JSON
)

func (u boundUnit) String() string {
	return [...]string{"a count", "characters", "bytes"}[u]
}

// manifestBound is one of manifestbounds.go's constants and where Core states it: a named constant in a file (core,
// whose value is read from its declaration and whose unit from every comparison Core makes against it), or a number
// written in a comparison (literal: a regexp whose one group is the number, and whose own form says the unit).
// evidence, when set, is what shows the unit where Core compares through a helper or a running total.
type manifestBound struct {
	name     string
	got      int
	unit     boundUnit
	file     string // relative to core/internal
	core     string
	literal  string
	evidence []string
}

func manifestBounds() []manifestBound {
	const (
		manifest = "plugin/schema_manifest.go"
		mobile   = "plugin/schema_mobile.go"
		form     = "plugin/adminform.go"
	)
	return []manifestBound{
		{name: "MaxManifestAdminPages", got: MaxManifestAdminPages, file: manifest, core: "maxAdminPages"},
		{name: "MaxManifestPageFields", got: MaxManifestPageFields, file: manifest, core: "maxPageFields"},
		{name: "MaxManifestReportParams", got: MaxManifestReportParams, file: "plugin/adminreport.go", core: "maxReportParams"},
		{name: "MaxManifestListColumns", got: MaxManifestListColumns, file: manifest, core: "maxListColumns"},
		{name: "MaxManifestRowActions", got: MaxManifestRowActions, file: manifest, core: "maxRowActions"},
		{name: "MaxManifestListFilters", got: MaxManifestListFilters, file: manifest, core: "maxListFilters"},
		{name: "MaxManifestFilterChoices", got: MaxManifestFilterChoices, file: manifest, core: "maxFilterChoices"},
		{name: "MaxManifestFilterChoiceLen", got: MaxManifestFilterChoiceLen, unit: unitRunes, file: manifest, core: "maxFilterChoiceRunes"},
		{name: "MaxManifestConditionValues", got: MaxManifestConditionValues, file: manifest, core: "maxConditionValues"},
		{name: "MaxManifestSettingChoices", got: MaxManifestSettingChoices, file: manifest, core: "maxSettingChoices"},
		{name: "MaxManifestSettingDefaultBytes", got: MaxManifestSettingDefaultBytes, unit: unitBytes, file: manifest, core: "maxSettingDefaultBytes"},
		{name: "MaxManifestLabelLen", got: MaxManifestLabelLen, unit: unitRunes, file: manifest, core: "maxDeclaredLabelLen"},
		{name: "MaxManifestHelpLen", got: MaxManifestHelpLen, unit: unitRunes, file: manifest, core: "maxDeclaredHelpLen"},

		{name: "MaxManifestFormFields", got: MaxManifestFormFields, file: form, core: "maxFormFields"},
		{name: "MaxManifestFormSections", got: MaxManifestFormSections, file: form, core: "maxFormSections"},
		{name: "MaxManifestRepeaterFields", got: MaxManifestRepeaterFields, file: form, core: "maxRepeaterFields"},

		{name: "MaxManifestDescriptionLen", got: MaxManifestDescriptionLen, unit: unitRunes, file: mobile, core: "maxDescriptionLen"},
		{name: "MaxManifestConfirmLen", got: MaxManifestConfirmLen, unit: unitRunes, file: mobile, core: "maxConfirmLen"},
		{name: "MaxManifestMetrics", got: MaxManifestMetrics, file: mobile, core: "maxMetrics"},
		{name: "MaxManifestBadges", got: MaxManifestBadges, file: mobile, core: "maxBadges"},
		{name: "MaxManifestRowSubtitle", got: MaxManifestRowSubtitle, file: mobile, core: "maxRowSubtitle"},
		{name: "MaxManifestHeadline", got: MaxManifestHeadline, file: mobile, core: "maxHeadline"},
		{name: "MaxManifestMobileLabelLen", got: MaxManifestMobileLabelLen, unit: unitRunes, file: mobile,
			literal: `utf8\.RuneCountInString\(mb\.Label\) > ([0-9]+) `},
		{name: "MaxManifestPickActions", got: MaxManifestPickActions, file: mobile, core: "maxPickActions"},
		{name: "MaxManifestPickActionLabelLen", got: MaxManifestPickActionLabelLen, unit: unitRunes, file: mobile,
			literal: `a\.Label == "" \|\| utf8\.RuneCountInString\(a\.Label\) > ([0-9]+) `},
		{name: "MaxManifestPickTools", got: MaxManifestPickTools, file: mobile, core: "maxPickTools"},
		{name: "MaxManifestPickSentenceLen", got: MaxManifestPickSentenceLen, unit: unitRunes, file: mobile, core: "maxPickSentence"},
		{name: "MaxManifestPickEffectLen", got: MaxManifestPickEffectLen, unit: unitRunes, file: mobile, core: "maxPickEffect"},

		{name: "MaxManifestTranslationLocales", got: MaxManifestTranslationLocales, file: manifest, core: "maxTranslationLocales"},
		{name: "MaxManifestTranslationStrings", got: MaxManifestTranslationStrings, file: manifest, core: "maxTranslationStrings"},
		{name: "MaxManifestTranslationLen", got: MaxManifestTranslationLen, unit: unitRunes, file: manifest, core: "maxTranslationLen"},
		{name: "MaxManifestTranslationLocaleBytes", got: MaxManifestTranslationLocaleBytes, unit: unitBytes, file: manifest,
			core:     "maxTranslationLocaleBytes",
			evidence: []string{"size += len(src) + len(translated)", "if size > maxTranslationLocaleBytes {"}},
		{name: "MaxManifestTranslationBytes", got: MaxManifestTranslationBytes, unit: unitBytes, file: manifest, core: "maxTranslationBytes",
			evidence: []string{"total += size", "if total > maxTranslationBytes {"}},

		{name: "MaxManifestEditorCommands", got: MaxManifestEditorCommands, file: manifest, core: "maxEditorCommands"},
		{name: "MaxManifestCommandKeywords", got: MaxManifestCommandKeywords, file: manifest, core: "maxCommandKeywords"},
		{name: "MaxManifestEditorCommandContentBytes", got: MaxManifestEditorCommandContentBytes, unit: unitBytes, file: manifest,
			core: "maxEditorCommandContentBytes"},
		{name: "MaxManifestFieldTypes", got: MaxManifestFieldTypes, file: manifest, core: "maxFieldTypesPerPlugin"},
		{name: "MaxManifestAuthProviders", got: MaxManifestAuthProviders, file: manifest, core: "maxAuthProvidersPerPlugin"},
		{name: "MaxManifestWebhookPaths", got: MaxManifestWebhookPaths, file: manifest, core: "maxWebhookPaths"},
		{name: "MaxManifestWebhookPathLen", got: MaxManifestWebhookPathLen, unit: unitBytes, file: manifest,
			literal: `case len\(w\) > ([0-9]+):`},
		{name: "MaxManifestFormReplacements", got: MaxManifestFormReplacements, file: manifest,
			literal: `if len\(list\) > ([0-9]+) \{\s+add\("form_replacements"`},
		{name: "MaxManifestFormSlugs", got: MaxManifestFormSlugs, file: manifest,
			literal: `len\(replacement\.FormSlugs\) > ([0-9]+) `},
		{name: "MaxManifestSearchNameLen", got: MaxManifestSearchNameLen, unit: unitRunes, file: manifest,
			literal: `case utf8\.RuneCountInString\(name\) > ([0-9]+):\s+p\.add\("search\.name"`},

		// Not a count (owner D4, Core's agent-control C4): the bytes of one plugin's labels, descriptions and input
		// schemas together. The install measures the words (validateAbilities' running total), Init the schemas too
		// (overAbilityBudget) — both are len, so bytes.
		{name: "MaxManifestAbilityDefinitionBytes", got: MaxManifestAbilityDefinitionBytes, unit: unitBytes,
			file: "plugin/schema_abilities.go", core: "maxAbilityDefinitionBytes",
			evidence: []string{"words += len(a.Label) + len(a.Description)", "if words > maxAbilityDefinitionBytes {",
				"total += len(a.Label) + len(a.Description) + len(a.InputSchema)"}},
		{name: "MaxManifestAbilityDescriptionLen", got: MaxManifestAbilityDescriptionLen, unit: unitRunes, file: "plugin/schema_abilities.go",
			core: "maxAbilityDescriptionLen"},
		{name: "MaxManifestVisitorTools", got: MaxManifestVisitorTools, file: "plugin/schema_visitortools.go", core: "maxVisitorTools"},
		{name: "MaxManifestVisitorToolDescriptionLen", got: MaxManifestVisitorToolDescriptionLen, unit: unitRunes,
			file: "plugin/schema_visitortools.go", core: "maxVisitorToolDescriptionLen"},
		{name: "MaxManifestVisitorToolPaths", got: MaxManifestVisitorToolPaths, file: "plugin/schema_visitortools.go", core: "maxVisitorToolPaths"},
		{name: "MaxManifestVisitorToolPathLen", got: MaxManifestVisitorToolPathLen, unit: unitBytes, file: "plugin/schema_visitortools.go",
			literal: `strings\.ContainsAny\(p, "\?#"\) \|\| len\(p\) > ([0-9]+) `},
		{name: "MaxManifestSiteFiles", got: MaxManifestSiteFiles, file: "plugin/schema_visitortools.go", core: "maxSiteFiles"},

		{name: "MaxManifestTables", got: MaxManifestTables, file: "plugin/schema_tables.go", core: "maxTablesPerPlugin"},
		{name: "MaxManifestTableColumns", got: MaxManifestTableColumns, file: "plugin/schema_tables.go",
			literal: `if len\(t\.Columns\) > ([0-9]+) \{`},
		{name: "MaxManifestTableNameLen", got: MaxManifestTableNameLen, unit: unitBytes, file: "plugin/schema_tables.go",
			literal: `func validIdent\(s string\) bool \{\s+if len\(s\) < 1 \|\| len\(s\) > ([0-9]+) \{`},
		{name: "MaxManifestIndexesPerTable", got: MaxManifestIndexesPerTable, file: "plugin/schema_tables.go", core: "maxIndexesPerTable"},
		{name: "MaxManifestIndexColumns", got: MaxManifestIndexColumns, file: "plugin/schema_tables.go", core: "maxIndexColumns"},

		{name: "MaxManifestEmailTemplates", got: MaxManifestEmailTemplates, file: "plugin/schema_emailtemplates.go", core: "maxEmailTemplates"},
		{name: "MaxManifestEmailVariables", got: MaxManifestEmailVariables, file: "plugin/schema_emailtemplates.go", core: "maxEmailVariables"},
		{name: "MaxManifestEmailSampleBytes", got: MaxManifestEmailSampleBytes, unit: unitBytes, file: "plugin/schema_emailtemplates.go",
			core: "maxEmailSampleBytes"},
		{name: "MaxManifestEmailLocales", got: MaxManifestEmailLocales, file: "plugin/schema_emailtemplates.go", core: "maxEmailLocales"},
		{name: "MaxManifestEmailSubjectLen", got: MaxManifestEmailSubjectLen, unit: unitRunes, file: "plugin/schema_emailtemplates.go",
			core: "maxEmailSubjectLen"},
		{name: "MaxManifestEmailBodyBytes", got: MaxManifestEmailBodyBytes, unit: unitBytes, file: "plugin/schema_emailtemplates.go",
			core: "maxEmailBodyBytes"},

		{name: "MaxManifestEmits", got: MaxManifestEmits, file: "plugin/schema_emits.go", core: "maxEmitDecls"},
		{name: "MaxManifestEmitFields", got: MaxManifestEmitFields, file: "plugin/schema_emits.go", core: "maxEmitFields"},
		{name: "MaxManifestEmitTypeLen", got: MaxManifestEmitTypeLen, unit: unitBytes, file: "plugin/schema_emits.go", core: "maxEmitTypeLen"},
		{name: "MaxManifestNotifications", got: MaxManifestNotifications, file: "plugin/schema_notifications.go", core: "maxNotificationDecls"},
		{name: "MaxManifestNoticeTitleLen", got: MaxManifestNoticeTitleLen, unit: unitRunes, file: "plugin/schema_notifications.go",
			core: "maxNoticeTitleLen", evidence: []string{"if utf8.RuneCountInString(s) > limit {", `checkWords("title", n.Title, maxNoticeTitleLen, true)`}},
		{name: "MaxManifestNoticeBodyLen", got: MaxManifestNoticeBodyLen, unit: unitRunes, file: "plugin/schema_notifications.go",
			core: "maxNoticeBodyLen", evidence: []string{"if utf8.RuneCountInString(s) > limit {", `checkWords("body", n.Body, maxNoticeBodyLen, false)`}},
		{name: "MaxManifestProcessing", got: MaxManifestProcessing, file: "plugin/schema_processing.go", core: "maxProcessingDecls"},
		{name: "MaxManifestProcessingTextLen", got: MaxManifestProcessingTextLen, unit: unitRunes, file: "plugin/schema_processing.go",
			core: "maxProcessingTextLen"},

		{name: "MaxManifestNetworkHosts", got: MaxManifestNetworkHosts, file: "plugin/schema_network.go", core: "maxNetworkHosts"},
		{name: "MaxManifestContracts", got: MaxManifestContracts, file: "plugin/schema_services.go", core: "maxDeclaredContracts"},
		{name: "MaxManifestRecommends", got: MaxManifestRecommends, file: "plugin/schema_setup.go", core: "maxRecommends"},
		{name: "MaxManifestRecommendationWhyLen", got: MaxManifestRecommendationWhyLen, unit: unitRunes, file: "plugin/schema_setup.go",
			core: "maxRecommendationWhy"},
		{name: "MaxManifestPlanLimits", got: MaxManifestPlanLimits, file: "plugin/schema_limits.go", core: "maxPlanLimits"},
		{name: "MaxManifestPlanLimitLabelLen", got: MaxManifestPlanLimitLabelLen, unit: unitRunes, file: "plugin/schema_limits.go",
			literal: `utf8\.RuneCountInString\(l\.Label\) > ([0-9]+) `},

		{name: "MaxManifestContentTypes", got: MaxManifestContentTypes, file: "plugin/schema_contenttypes.go", core: "maxPluginContentTypes"},
		{name: "MaxManifestContentTypeFields", got: MaxManifestContentTypeFields, file: "plugin/schema_contenttypes.go",
			core: "maxPluginContentTypeFields"},
		{name: "MaxManifestContentTypeGroups", got: MaxManifestContentTypeGroups, file: "plugin/schema_contenttypes.go",
			core: "maxPluginContentTypeGroups"},
		{name: "MaxManifestTaxonomies", got: MaxManifestTaxonomies, file: "plugin/schema_taxonomies.go", core: "maxPluginTaxonomies"},
		{name: "MaxManifestTaxonomyKeyLen", got: MaxManifestTaxonomyKeyLen, unit: unitBytes, file: "plugin/schema_contenttypes.go",
			literal: `func taxonomyKeyValid\(s string\) bool \{\s+if len\(s\) < 1 \|\| len\(s\) > ([0-9]+) \{`},
		{name: "MaxManifestTaxonomyAppliesTo", got: MaxManifestTaxonomyAppliesTo, file: "plugin/schema_taxonomies.go", core: "maxTaxonomyAppliesTo"},
		{name: "MaxManifestTemplates", got: MaxManifestTemplates, file: "plugin/schema_templates.go", core: "maxPluginTemplates"},
		{name: "MaxManifestTemplateBytes", got: MaxManifestTemplateBytes, unit: unitBytes, file: "pagebuilder/plugintemplate.go",
			core: "MaxPluginTemplateBytes"},

		{name: "MaxManifestLegalNoticeLen", got: MaxManifestLegalNoticeLen, unit: unitRunes, file: "plugin/schema_notice.go", core: "maxLegalNoticeLen"},
		{name: "MaxManifestAccountNameLen", got: MaxManifestAccountNameLen, unit: unitRunes, file: "plugin/schema_account.go", core: "maxAccountName"},
		{name: "MaxManifestAccountHostLen", got: MaxManifestAccountHostLen, unit: unitBytes, file: "plugin/schema_account.go",
			literal: `case len\(a\.Host\) > ([0-9]+) `},
		{name: "MaxManifestBytes", got: MaxManifestBytes, unit: unitBytes, file: "plugin/schema_package.go", core: "maxManifestBytes"},
	}
}

// manifestValidationSources is every file of Core's manifest validation, by path under core/internal: where a bound is
// declared, and where Core compares against it (the unit). The schema files are listed from Core's own directory, so a
// new one is read without this guard being touched.
func manifestValidationSources(t *testing.T) map[string]string {
	t.Helper()
	coreSource(t, "go.mod") // skips when Core is not checked out
	entries, err := os.ReadDir(filepath.Join("..", "core", "internal", "plugin"))
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"plugin/adminform.go", "plugin/adminreport.go", "plugin/tables.go", "email/check.go", "pagebuilder/plugintemplate.go"}
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "schema_") && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			paths = append(paths, "plugin/"+n)
		}
	}
	out := map[string]string{}
	for _, p := range paths {
		out[p] = coreSource(t, append([]string{"internal"}, strings.Split(p, "/")...)...)
	}
	return out
}

// coreConstValue is the value of a constant Core declares in sources: a number, a shift of two, or another constant —
// unqualified, or by its package (email.MaxSubjectRunes) — read where it is declared. chain is every name it went
// through, so the unit is read from the comparisons against any of them.
func coreConstValue(t *testing.T, sources map[string]string, file, name string, chain *[]string) int {
	t.Helper()
	*chain = append(*chain, name)
	decl := regexp.MustCompile(`(?m)^\s*(?:const\s+)?` + regexp.QuoteMeta(name) + `\s*=\s*([^=\n][^\n]*?)\s*(?://.*)?$`)
	m := decl.FindStringSubmatch(sources[file])
	if m == nil {
		// Declared in another file of Core's validation (a package-qualified name, or one beside it).
		for _, p := range sortedKeys(sources) {
			if m = decl.FindStringSubmatch(sources[p]); m != nil {
				file = p
				break
			}
		}
	}
	if m == nil {
		t.Fatalf("core's %s no longer declares %s — repoint TestTheManifestBoundsAreCores", file, name)
	}
	expr := strings.TrimSpace(m[1])
	if a, b, ok := strings.Cut(expr, "<<"); ok {
		return coreNumber(t, file, name, strings.TrimSpace(a)) << coreNumber(t, file, name, strings.TrimSpace(b))
	}
	if regexp.MustCompile(`^[0-9_]+$`).MatchString(expr) {
		return coreNumber(t, file, name, expr)
	}
	if ref := regexp.MustCompile(`^(?:[a-z]+\.)?([A-Za-z]\w*)$`).FindStringSubmatch(expr); ref != nil {
		return coreConstValue(t, sources, file, ref[1], chain)
	}
	t.Fatalf("core's %s declares %s = %s, which this guard cannot read — teach it", file, name, expr)
	return 0
}

func coreNumber(t *testing.T, file, name, s string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.ReplaceAll(s, "_", ""))
	if err != nil {
		t.Fatalf("core's %s declares %s with %q, which this guard cannot read — teach it", file, name, s)
	}
	return n
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The left side of a comparison: a call with at most one call nested in it.
const boundOperand = `\([^()\n]*(?:\([^()\n]*\))?[^()\n]*\)`

// comparisonUnits is the unit of every comparison in src whose right side matches bound: runes for
// utf8.RuneCountInString or len([]rune(…)), bytes-or-count for any other len(…).
func comparisonUnits(src, bound string) (runes, lens []string) {
	runes = append(runes, regexp.MustCompile(`utf8\.RuneCountInString`+boundOperand+`\s*>=?\s*`+bound).FindAllString(src, -1)...)
	runes = append(runes, regexp.MustCompile(`len\(\[\]rune`+boundOperand+`\)\s*>=?\s*`+bound).FindAllString(src, -1)...)
	for _, m := range regexp.MustCompile(`len`+boundOperand+`\s*>=?\s*`+bound).FindAllString(src, -1) {
		if !strings.HasPrefix(m, "len([]rune(") {
			lens = append(lens, m)
		}
	}
	return runes, lens
}

// Gap #9 of the shop's Unit 12 end review: the shop held its plugin.json to Core's manifest bounds through sixty
// constants copied by hand, and some had gone stale (a translation string was 400 bytes in the copy and 500
// characters in Core). Every bound manifestbounds.go exports is Core's number AND Core's unit, read from Core's
// validation, so a plugin's own test can use them and a change in Core turns this red, not the plugin's install.
func TestTheManifestBoundsAreCores(t *testing.T) {
	sources := manifestValidationSources(t)
	for _, b := range manifestBounds() {
		src, ok := sources[b.file]
		if !ok {
			t.Fatalf("%s: %s is not one of the manifest validation sources this guard reads", b.name, b.file)
		}
		var runes, lens []string
		if b.literal != "" {
			m := regexp.MustCompile(b.literal).FindAllStringSubmatch(src, -1)
			if len(m) != 1 {
				t.Errorf("%s: core's %s has %d matches of %q, want one — repoint this guard", b.name, b.file, len(m), b.literal)
				continue
			}
			if n := coreNumber(t, b.file, b.name, m[0][1]); n != b.got {
				t.Errorf("%s is %d, core's %s says %d", b.name, b.got, b.file, n)
			}
			runes, lens = comparisonUnits(m[0][0], `[0-9]+`)
		} else {
			var chain []string
			if n := coreConstValue(t, sources, b.file, b.core, &chain); n != b.got {
				t.Errorf("%s is %d, core's %s (%s) is %d", b.name, b.got, b.core, b.file, n)
			}
			for _, name := range chain {
				for _, p := range sortedKeys(sources) {
					r, l := comparisonUnits(sources[p], `\b`+regexp.QuoteMeta(name)+`\b`)
					runes, lens = append(runes, r...), append(lens, l...)
				}
			}
		}
		for _, e := range b.evidence {
			if !strings.Contains(src, e) {
				t.Errorf("%s: core's %s no longer has %q, which is what shows it counts in %s — re-read it", b.name, b.file, e, b.unit)
			}
			r, l := comparisonUnits(e, `\w+`)
			runes, lens = append(runes, r...), append(lens, l...)
		}
		switch {
		case b.unit == unitRunes && len(lens) > 0:
			t.Errorf("%s says characters, but Core counts it with len: %q", b.name, lens)
		case b.unit == unitRunes && len(runes) == 0:
			t.Errorf("%s says characters, and no comparison in Core's validation counts characters against it", b.name)
		case b.unit != unitRunes && len(runes) > 0:
			t.Errorf("%s says %s, but Core counts it in characters: %q", b.name, b.unit, runes)
		case b.unit == unitBytes && len(lens) == 0 && len(b.evidence) == 0:
			t.Errorf("%s says bytes, and no comparison in Core's validation measures a len against it", b.name)
		}
	}
}

// Every constant of manifestbounds.go is in the guard's table, once, and the guide's table gives each its value and
// its unit — so the bound an author reads in the guide is the one the constant holds.
func TestEveryManifestBoundIsHeldAndDocumented(t *testing.T) {
	declared := regexp.MustCompile(`(?m)^\t(MaxManifest\w*)\s+= `).FindAllStringSubmatch(readText(t, "manifestbounds.go"), -1)
	held := map[string]manifestBound{}
	for _, b := range manifestBounds() {
		if _, twice := held[b.name]; twice {
			t.Errorf("%s is in the guard's table twice", b.name)
		}
		held[b.name] = b
	}
	if len(declared) != len(held) {
		t.Errorf("manifestbounds.go declares %d bounds, the guard holds %d", len(declared), len(held))
	}
	guide := readText(t, "docs/PLUGIN_SDK.md")
	for _, d := range declared {
		b, ok := held[d[1]]
		if !ok {
			t.Errorf("%s is not held to Core by TestTheManifestBoundsAreCores", d[1])
			continue
		}
		row := "| `" + b.name + "` | " + b.unit.guideValue(b.got) + " |"
		if !strings.Contains(guide, row) {
			t.Errorf("PLUGIN_SDK.md has no row starting %q", row)
		}
	}
}

// guideValue is a bound as the guide's table writes it: a count bare, characters named, bytes in KiB or MiB when they
// are a whole number of them.
func (u boundUnit) guideValue(n int) string {
	switch {
	case u == unitRunes:
		return addThousands(n) + " characters"
	case u == unitBytes && n%(1<<20) == 0:
		return strconv.Itoa(n>>20) + " MiB"
	case u == unitBytes && n%(1<<10) == 0:
		return strconv.Itoa(n>>10) + " KiB"
	case u == unitBytes:
		return addThousands(n) + " bytes"
	}
	return addThousands(n)
}
