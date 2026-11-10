package nilda

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/nilda-dev/plugin-sdk/siteimport"
)

func siteReaderContract(t *testing.T) ServiceContract {
	t.Helper()
	all, err := Contracts()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.Name == ContractSiteReader {
			return c
		}
	}
	t.Fatalf("%s is not among the contracts", ContractSiteReader)
	return ServiceContract{}
}

// Only Core may ask a reader (it carries the owner's password), and a half-delivered read is not a read.
func TestOnlyCoreAsksASiteReaderAndItBlocks(t *testing.T) {
	c := siteReaderContract(t)
	if !c.Experimental() {
		t.Error("the contract is not marked experimental")
	}
	for _, name := range []string{SiteReaderSources, SiteReaderStart, SiteReaderFeed, SiteReaderNext, SiteReaderCancel} {
		m, ok := c.Method(name)
		if !ok {
			t.Fatalf("method %s is missing", name)
		}
		if m.Agent != "none" || m.Fallback != "block" || m.AnsweredBy != "provider" || m.Write != nil {
			t.Errorf("%s: agent %q fallback %q answered_by %q write %v — a read is Core's call, never an assistant tool, never skipped or cached",
				name, m.Agent, m.Fallback, m.AnsweredBy, m.Write)
		}
		if !m.Idempotent {
			t.Errorf("%s is not idempotent: a retry after a timeout must not change the read", name)
		}
	}
}

// A real piece — a Site in the snapshot encoding — travels through the contract's `next` answer and back into a Site.
func TestAPieceOfARealSiteIsAValidAnswerToNext(t *testing.T) {
	site := siteimport.Site{SiteKey: "old.example", BaseURL: "https://old.example", Items: []siteimport.Item{{SourceID: "old.example#1", Title: "Hello"}}}
	var enc bytes.Buffer
	if err := siteimport.Encode(&enc, &site); err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(enc.Bytes(), &members); err != nil {
		t.Fatal(err)
	}
	answer, _ := json.Marshal(SiteReaderPiece{Seq: 0, SnapshotVersion: SiteReaderSnapshotVersion, Last: true, Piece: members,
		Progress: &SiteReaderProgress{Phase: "items", Done: 1, Total: 1},
		Losses:   []SiteReaderLoss{{Code: "meta_withheld", Count: 2, Data: map[string]json.RawMessage{"names": json.RawMessage(`["_edit_lock"]`)}}}})
	m, _ := siteReaderContract(t).Method(SiteReaderNext)
	if err := m.ValidateOutput(answer); err != nil {
		t.Fatalf("a valid piece was refused: %v", err)
	}
	var back SiteReaderPiece
	if err := json.Unmarshal(answer, &back); err != nil {
		t.Fatal(err)
	}
	piece, _ := json.Marshal(back.Piece)
	var got siteimport.Site
	if err := siteimport.Decode(bytes.NewReader(piece), &got); err != nil || got.SiteKey != "old.example" || len(got.Items) != 1 {
		t.Fatalf("the piece did not come back as the site: %+v %v", got, err)
	}
	if err := m.ValidateInput([]byte(`{"read_id":"r1"}`)); err == nil {
		t.Error("a next without its seq was accepted")
	}
	if err := m.ValidateOutput([]byte(`{"seq":0,"last":true}`)); err == nil {
		t.Error("an answer without its piece and snapshot_version was accepted")
	}
}

// The snapshot version is part of the contract: Core compares it for equality, so a reader that sends a different number is refused
// as a whole. Version 5 added an author's bio and link (siteimport.Author); the number moves with the model, never alone.
func TestTheSnapshotVersionIsFiveAndTheModelHasWhatFiveAdded(t *testing.T) {
	if SiteReaderSnapshotVersion != 5 {
		t.Fatalf("SiteReaderSnapshotVersion = %d, want 5 (bio and link on an author)", SiteReaderSnapshotVersion)
	}
	author := reflect.TypeOf(siteimport.Author{})
	for _, name := range []string{"Bio", "Link"} {
		f, ok := author.FieldByName(name)
		if !ok || f.Type.Kind() != reflect.String || !strings.HasSuffix(f.Tag.Get("json"), ",omitempty") {
			t.Errorf("siteimport.Author.%s is missing, not a string or not omitempty: version 4 bytes must not change (%v)", name, f)
		}
	}
	if _, ok := author.FieldByName("Slug"); ok {
		t.Error("siteimport.Author has a Slug: a guest author has no slug, so a slug would be a field nothing reads")
	}
}
