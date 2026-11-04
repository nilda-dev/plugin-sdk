package nilda

// Core's bounds on a manifest: how many of each thing plugin.json may declare, and how long each text in it may be.
// Core refuses a WHOLE manifest at install for any one bound crossed (and `nilda plugin check` refuses it on your
// machine), so a plugin's own test can hold its plugin.json to these and hear about a crossed bound at its keyboard.
//
// Each is Core's own number, held to Core's source by TestTheManifestBoundsAreCores, which also holds the unit Core
// counts in. The unit is said on each, because it is the half that bites:
//
//   - a count is a number of entries in a list or a map;
//   - characters are Unicode characters (runes), so a Persian or Arabic text has as many as an English one of the
//     same letters;
//   - bytes are UTF-8 bytes, where a Persian or Arabic letter takes two.
//
// These are the bounds Core states as a number. A key or a name whose shape Core holds to a pattern — a page's key,
// a content type's key, a limit's name, a locale code — is not here: the pattern is the rule, and `nilda plugin
// check` says it.
const (
	// The admin section (`admin_pages`).

	// MaxManifestAdminPages bounds admin_pages: a count.
	MaxManifestAdminPages = 40
	// MaxManifestPageFields bounds a settings page's fields: a count.
	MaxManifestPageFields = 48
	// MaxManifestReportParams bounds a report or calendar page's fields (its filters): a count.
	MaxManifestReportParams = 20
	// MaxManifestListColumns bounds a list page's columns, and its search columns, each: a count.
	MaxManifestListColumns = 20
	// MaxManifestRowActions bounds a list page's row actions: a count.
	MaxManifestRowActions = 8
	// MaxManifestListFilters bounds a list page's filters: a count.
	MaxManifestListFilters = 4
	// MaxManifestFilterChoices bounds a choice filter's values: a count.
	MaxManifestFilterChoices = 20
	// MaxManifestFilterChoiceLen bounds one of a choice filter's values: characters.
	MaxManifestFilterChoiceLen = 120
	// MaxManifestConditionValues bounds a row action's shown_when, its in and its not_in each: a count.
	MaxManifestConditionValues = 20
	// MaxManifestSettingChoices bounds a field's choices, on a settings page and in a form: a count.
	MaxManifestSettingChoices = 100
	// MaxManifestSettingDefaultBytes bounds a setting's default, written as JSON: bytes.
	MaxManifestSettingDefaultBytes = 4 << 10
	// MaxManifestLabelLen bounds the plugin's name and every label, placeholder, unit, choice, group, icon, title and
	// keyword it declares: characters.
	MaxManifestLabelLen = 120
	// MaxManifestHelpLen bounds every help text: characters.
	MaxManifestHelpLen = 500

	// A list page's form (`form`).

	// MaxManifestFormFields bounds a form's fields, a repeater's own counted: a count.
	MaxManifestFormFields = 80
	// MaxManifestFormSections bounds a form's sections: a count.
	MaxManifestFormSections = 12
	// MaxManifestRepeaterFields bounds one repeater's own fields: a count.
	MaxManifestRepeaterFields = 16

	// The plugin in the Nilda app (`description`, `mobile`, a page's metrics, badges and row).

	// MaxManifestDescriptionLen bounds the plugin's description, a mail's and an emitted field's: characters.
	MaxManifestDescriptionLen = 300
	// MaxManifestConfirmLen bounds a row action's confirm, the question before it acts: characters.
	MaxManifestConfirmLen = 300
	// MaxManifestMetrics bounds a report page's metrics: a count.
	MaxManifestMetrics = 8
	// MaxManifestBadges bounds a column's badges, its labels and its types, each: a count.
	MaxManifestBadges = 40
	// MaxManifestRowSubtitle bounds the columns a page's mobile.row.subtitle names: a count.
	MaxManifestRowSubtitle = 2
	// MaxManifestHeadline bounds mobile.headline: a count.
	MaxManifestHeadline = 3
	// MaxManifestMobileLabelLen bounds mobile.label: characters.
	MaxManifestMobileLabelLen = 40
	// MaxManifestPickActions bounds a page's mobile.pick actions: a count.
	MaxManifestPickActions = 4
	// MaxManifestPickActionLabelLen bounds a mobile.pick action's label: characters.
	MaxManifestPickActionLabelLen = 24
	// MaxManifestPickTools bounds the tools a mobile.pick names: a count.
	MaxManifestPickTools = 12
	// MaxManifestPickSentenceLen bounds the sentence a mobile.pick chip writes in the chat box: characters.
	MaxManifestPickSentenceLen = 120
	// MaxManifestPickEffectLen bounds a mobile.pick action's effect, what visitors will see change: characters.
	MaxManifestPickEffectLen = 160

	// Translations (`translations`).

	// MaxManifestTranslationLocales bounds the languages translations declares: a count.
	MaxManifestTranslationLocales = 12
	// MaxManifestTranslationStrings bounds one language's strings: a count.
	MaxManifestTranslationStrings = 1500
	// MaxManifestTranslationLen bounds one string, its source and its translation each: characters.
	MaxManifestTranslationLen = 500
	// MaxManifestTranslationLocaleBytes bounds one language's sources and translations together — what an admin reading
	// in it receives: bytes.
	MaxManifestTranslationLocaleBytes = 192 << 10
	// MaxManifestTranslationBytes bounds every source and translation of every language together, as stored: bytes.
	MaxManifestTranslationBytes = 1 << 20

	// The rest of what the plugin adds to the site (`editor_commands`, `fields`, `auth_providers`, `webhook_paths`,
	// `form_replacements`, `search`).

	// MaxManifestEditorCommands bounds editor_commands: a count.
	MaxManifestEditorCommands = 24
	// MaxManifestCommandKeywords bounds one editor command's keywords: a count.
	MaxManifestCommandKeywords = 16
	// MaxManifestEditorCommandContentBytes bounds one editor command's content: bytes.
	MaxManifestEditorCommandContentBytes = 32 << 10
	// MaxManifestFieldTypes bounds the field types fields declares: a count.
	MaxManifestFieldTypes = 12
	// MaxManifestAuthProviders bounds auth_providers: a count.
	MaxManifestAuthProviders = 4
	// MaxManifestWebhookPaths bounds webhook_paths: a count.
	MaxManifestWebhookPaths = 8
	// MaxManifestWebhookPathLen bounds one of webhook_paths: bytes.
	MaxManifestWebhookPathLen = 128
	// MaxManifestFormReplacements bounds form_replacements: a count.
	MaxManifestFormReplacements = 4
	// MaxManifestFormSlugs bounds one form replacement's form_slugs: a count.
	MaxManifestFormSlugs = 8
	// MaxManifestSearchNameLen bounds search.name, the engine's name: characters.
	MaxManifestSearchNameLen = 40

	// What the AI can do (`abilities`, `visitor_tools`, `site_files`).

	// MaxManifestAbilities bounds abilities: a count.
	MaxManifestAbilities = 200
	// MaxManifestAbilityDescriptionLen bounds an ability's description: characters.
	MaxManifestAbilityDescriptionLen = 2000
	// MaxManifestVisitorTools bounds visitor_tools: a count.
	MaxManifestVisitorTools = 16
	// MaxManifestVisitorToolDescriptionLen bounds a visitor tool's description: characters.
	MaxManifestVisitorToolDescriptionLen = 1000
	// MaxManifestVisitorToolPaths bounds a visitor tool's paths: a count.
	MaxManifestVisitorToolPaths = 8
	// MaxManifestVisitorToolPathLen bounds one of a visitor tool's paths: bytes.
	MaxManifestVisitorToolPathLen = 200
	// MaxManifestSiteFiles bounds site_files: a count.
	MaxManifestSiteFiles = 8

	// Tables (`tables`).

	// MaxManifestTables bounds tables: a count.
	MaxManifestTables = 128
	// MaxManifestTableColumns bounds one table's columns: a count.
	MaxManifestTableColumns = 64
	// MaxManifestTableNameLen bounds a table's name and a column's: bytes (the name is lowercase ASCII).
	MaxManifestTableNameLen = 48
	// MaxManifestIndexesPerTable bounds one table's indexes, counting its unique columns and the index a list page's
	// order_by adds: a count.
	MaxManifestIndexesPerTable = 16
	// MaxManifestIndexColumns bounds one index's columns: a count.
	MaxManifestIndexColumns = 32

	// Mail (`email_templates`).

	// MaxManifestEmailTemplates bounds email_templates: a count.
	MaxManifestEmailTemplates = 64
	// MaxManifestEmailVariables bounds one template's variables: a count.
	MaxManifestEmailVariables = 64
	// MaxManifestEmailSampleBytes bounds one variable's sample, written as JSON: bytes.
	MaxManifestEmailSampleBytes = 16 << 10
	// MaxManifestEmailLocales bounds the languages one template's defaults are written in: a count.
	MaxManifestEmailLocales = 64
	// MaxManifestEmailSubjectLen bounds a default's subject: characters.
	MaxManifestEmailSubjectLen = 200
	// MaxManifestEmailBodyBytes bounds a default's text, and its HTML, each: bytes.
	MaxManifestEmailBodyBytes = 32 << 10

	// Events, notices and personal data (`emits`, `notifications`, `processing`).

	// MaxManifestEmits bounds emits: a count.
	MaxManifestEmits = 32
	// MaxManifestEmitFields bounds one event's fields: a count.
	MaxManifestEmitFields = 64
	// MaxManifestEmitTypeLen bounds an event's type: bytes (the type is lowercase ASCII).
	MaxManifestEmitTypeLen = 96
	// MaxManifestNotifications bounds notifications: a count.
	MaxManifestNotifications = 16
	// MaxManifestNoticeTitleLen bounds a notice's title, before its {field}s are filled: characters.
	MaxManifestNoticeTitleLen = 120
	// MaxManifestNoticeBodyLen bounds a notice's body, before its {field}s are filled: characters.
	MaxManifestNoticeBodyLen = 200
	// MaxManifestProcessing bounds processing: a count.
	MaxManifestProcessing = 16
	// MaxManifestProcessingTextLen bounds a processing entry's what, recipient and retention, each: characters.
	MaxManifestProcessingTextLen = 400

	// What the plugin reaches and speaks (`network`, `provides`, `requires`, `uses`, `recommends`, `limits`).

	// MaxManifestNetworkHosts bounds network: a count.
	MaxManifestNetworkHosts = 32
	// MaxManifestContracts bounds provides, requires and uses, each: a count.
	MaxManifestContracts = 16
	// MaxManifestRecommends bounds recommends: a count.
	MaxManifestRecommends = 8
	// MaxManifestRecommendationWhyLen bounds a recommendation's why: characters.
	MaxManifestRecommendationWhyLen = 200
	// MaxManifestPlanLimits bounds limits: a count.
	MaxManifestPlanLimits = 16
	// MaxManifestPlanLimitLabelLen bounds a limit's label: characters.
	MaxManifestPlanLimitLabelLen = 80

	// Content (`content_types`, `taxonomies`, `templates`).

	// MaxManifestContentTypes bounds content_types: a count.
	MaxManifestContentTypes = 8
	// MaxManifestContentTypeFields bounds a content type's fields, and a field's sub-fields: a count.
	MaxManifestContentTypeFields = 48
	// MaxManifestContentTypeGroups bounds a content type's groups: a count.
	MaxManifestContentTypeGroups = 16
	// MaxManifestTaxonomies bounds taxonomies: a count.
	MaxManifestTaxonomies = 8
	// MaxManifestTaxonomyKeyLen bounds a taxonomy's key: bytes (the key is lowercase ASCII).
	MaxManifestTaxonomyKeyLen = 60
	// MaxManifestTaxonomyAppliesTo bounds the content types one taxonomy applies_to: a count.
	MaxManifestTaxonomyAppliesTo = 32
	// MaxManifestTemplates bounds templates, one per content type: a count.
	MaxManifestTemplates = 8
	// MaxManifestTemplateBytes bounds one template's tree, as JSON: bytes.
	MaxManifestTemplateBytes = 64 << 10

	// The rest (`legal_notice`, `account`, the file itself).

	// MaxManifestLegalNoticeLen bounds legal_notice: characters.
	MaxManifestLegalNoticeLen = 12000
	// MaxManifestAccountNameLen bounds account.name: characters.
	MaxManifestAccountNameLen = 80
	// MaxManifestAccountHostLen bounds account.host: bytes (a host name is ASCII).
	MaxManifestAccountHostLen = 253
	// MaxManifestBytes bounds plugin.json itself, in the package: bytes.
	MaxManifestBytes = 2 << 20
)
