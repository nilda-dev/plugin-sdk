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
	if MaxManifestTranslationLen != 500 {
		t.Errorf("length %d: Core #455 leaves it 500", MaxManifestTranslationLen)
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

// Core raised a language's strings from 1,500 to 3,000 on 2026-11-21 (owner: "if we need to raise it, raise it"; the shop
// was at 1,484 in each of its five languages). Every count from one past the old bound to the new one is inside it, one
// past the new one is not, and the byte bounds that guard a browser and the stored row did not move with it.
func TestTranslationStringBoundMirrorsCoreAt3000(t *testing.T) {
	const oldStrings, newStrings = 1500, 3000
	if MaxManifestTranslationStrings != newStrings {
		t.Errorf("MaxManifestTranslationStrings = %d, want %d (Core 2026-11-21)", MaxManifestTranslationStrings, newStrings)
	}
	within := func(n int) bool { return n <= MaxManifestTranslationStrings }
	for _, n := range []int{oldStrings + 1, newStrings} {
		if !within(n) {
			t.Errorf("a language of %d strings must pass", n)
		}
	}
	if within(newStrings + 1) {
		t.Errorf("a language of %d strings must be refused", newStrings+1)
	}
	if MaxManifestTranslationLocaleBytes != 768<<10 || MaxManifestTranslationBytes != 3<<20 {
		t.Errorf("the byte bounds moved with the count: %d a language, %d in all", MaxManifestTranslationLocaleBytes, MaxManifestTranslationBytes)
	}
}
