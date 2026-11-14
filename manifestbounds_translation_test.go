package nilda

import "testing"

// Core #455 (C4c) raised the translation bounds to four times the largest measured plugin: a language 768 KiB, all of them
// 3 MiB. A language the old 192 KiB bound refused is inside the new one; one over 768 KiB is still refused.
func TestTranslationByteBoundsMirrorCoreC4c(t *testing.T) {
	const oldLocale, oldTotal = 192 << 10, 1 << 20
	if MaxManifestTranslationLocaleBytes != 768<<10 {
		t.Errorf("MaxManifestTranslationLocaleBytes = %d, want 768 KiB (Core #455)", MaxManifestTranslationLocaleBytes)
	}
	if MaxManifestTranslationBytes != 3<<20 {
		t.Errorf("MaxManifestTranslationBytes = %d, want 3 MiB (Core #455)", MaxManifestTranslationBytes)
	}
	if MaxManifestTranslationStrings != 1500 || MaxManifestTranslationLen != 500 {
		t.Errorf("strings %d / length %d: Core #455 leaves them 1500 / 500", MaxManifestTranslationStrings, MaxManifestTranslationLen)
	}
	within := func(size, bound int) bool { return size <= bound }
	if !within(oldLocale+1, MaxManifestTranslationLocaleBytes) {
		t.Error("a language just over the old bound must pass")
	}
	if within(MaxManifestTranslationLocaleBytes+1, MaxManifestTranslationLocaleBytes) {
		t.Error("a language over the new bound must be refused")
	}
	if !within(oldTotal+1, MaxManifestTranslationBytes) || within(MaxManifestTranslationBytes+1, MaxManifestTranslationBytes) {
		t.Error("the total bound must admit just over 1 MiB and refuse just over 3 MiB")
	}
}
