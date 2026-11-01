package evidencebundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func sample() Bundle {
	return Bundle{Format: Format, Version: Version, Title: "Evidence — örnek 🧭", Sources: []Source{{Alias: "s1", Kind: "filesystem", DisplayName: "Notes"}}, Entries: []Entry{
		{SourceAlias: "s1", RelativePath: "folder/quote.md", MediaType: "text/markdown", ContentHash: strings.Repeat("a", 64), StartChar: 17, Text: "повтор 🧪\nİstanbul", Note: "заметка\nsecond"},
		{SourceAlias: "s1", RelativePath: "file.pdf", MediaType: "application/pdf", ContentHash: strings.Repeat("0", 64), Page: 2, StartChar: 9, Text: "повтор 🧪", Note: "different note"},
	}}
}

func TestDecodeRequiresExactNonNullWireFields(t *testing.T) {
	raw, err := Encode(sample())
	if err != nil {
		t.Fatal(err)
	}
	base := string(raw)
	cases := map[string]string{
		"empty format":               strings.Replace(base, `"format":"findrail-evidence-bundle"`, `"format":""`, 1),
		"null format":                strings.Replace(base, `"format":"findrail-evidence-bundle"`, `"format":null`, 1),
		"zero version":               strings.Replace(base, `"version":1`, `"version":0`, 1),
		"null version":               strings.Replace(base, `"version":1`, `"version":null`, 1),
		"case folded field":          strings.Replace(base, `"version":1`, `"version":1,"Version":0`, 1),
		"null note":                  strings.Replace(base, `"note":"different note"`, `"note":null`, 1),
		"unpaired high surrogate":    strings.Replace(base, `"title":`, `"unknown":"\ud800","title":`, 1),
		"unpaired surrogate in text": strings.Replace(base, `"note":"different note"`, `"note":"\ud800"`, 1),
		"unpaired low surrogate":     strings.Replace(base, `"note":"different note"`, `"note":"\udc00"`, 1),
		"null sources":               `{"format":"findrail-evidence-bundle","version":1,"title":"empty","sources":null,"entries":[]}`,
		"null entries":               `{"format":"findrail-evidence-bundle","version":1,"title":"empty","sources":[],"entries":null}`,
	}
	duplicate := sample()
	duplicate.Sources = append(duplicate.Sources, duplicate.Sources[0])
	value, err := json.Marshal(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	cases["duplicate source alias"] = string(value)
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(input)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid bundle, got %v", err)
			}
		})
	}
	if _, err := Encode(duplicate); !errors.Is(err, ErrInvalid) {
		t.Fatalf("encoder accepted duplicate alias: %v", err)
	}
}

func TestCanonicalEmptyArraysAndEscapedUnicode(t *testing.T) {
	raw, err := Encode(Bundle{Title: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"sources":[]`)) || !bytes.Contains(raw, []byte(`"entries":[]`)) {
		t.Fatalf("empty arrays must not encode as null: %s", raw)
	}
	b := sample()
	b.Entries[1].Note = "😀 replacement \ufffd literal \\ud800"
	raw, err = Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("😀"), []byte(`\ud83d\ude00`))
	got, err := Decode(bytes.NewReader(raw))
	if err != nil || !reflect.DeepEqual(got, b) {
		t.Fatalf("valid Unicode lost: %v %#v", err, got)
	}
}

func TestDecodeBoundsArraysBeforeTypedConstruction(t *testing.T) {
	b := sample()
	for len(b.Entries) <= MaxEntries {
		b.Entries = append(b.Entries, b.Entries[0])
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(bytes.NewReader(raw)); !errors.Is(err, ErrLimit) {
		t.Fatalf("entry cap: %v", err)
	}
	b = sample()
	for len(b.Sources) <= MaxSources {
		b.Sources = append(b.Sources, b.Sources[0])
	}
	raw, err = json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(bytes.NewReader(raw)); !errors.Is(err, ErrLimit) {
		t.Fatalf("source cap before duplicate alias validation: %v", err)
	}
}

func TestBundleTextBoundaries(t *testing.T) {
	b := sample()
	b.Entries = b.Entries[:1]
	b.Entries[0].Text = strings.Repeat("😀", 65536)
	b.Entries[0].Note = strings.Repeat("n", 2048)
	b.Entries[0].RelativePath = strings.Repeat("😀", 512)
	b.Title = strings.Repeat("t", 256)
	for len(b.Entries) < 8 {
		b.Entries = append(b.Entries, b.Entries[0])
	}
	raw, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(bytes.NewReader(raw))
	if err != nil || !reflect.DeepEqual(got, b) {
		t.Fatalf("valid boundaries: %v", err)
	}
	b.Entries = append(b.Entries, b.Entries[0])
	if _, err := Encode(b); !errors.Is(err, ErrLimit) {
		t.Fatalf("total text cap: %v", err)
	}
	b = sample()
	b.Entries[0].Text = strings.Repeat("a", 65537)
	if _, err := Encode(b); err == nil {
		t.Fatal("rune limit accepted")
	}
}

func FuzzDecodeCanonicalRoundTrip(f *testing.F) {
	raw, err := Encode(sample())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	f.Add([]byte(`{"format":"findrail-evidence-bundle","version":1,"title":"empty","sources":[],"entries":[]}`))
	f.Add([]byte(`{"title":"\ud800"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			return
		}
		b, err := Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		canonical, err := Encode(b)
		if err != nil {
			t.Fatalf("decoded value cannot encode: %v", err)
		}
		again, err := Decode(bytes.NewReader(canonical))
		if err != nil || !reflect.DeepEqual(b, again) {
			t.Fatalf("canonical round trip changed evidence: %v", err)
		}
	})
}
func TestRoundTripCanonicalAndDigest(t *testing.T) {
	b := sample()
	encoded, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Entries) != 2 || decoded.Entries[0].Text != "повтор 🧪\nİstanbul" || decoded.Entries[1].Page != 2 || decoded.Entries[1].Note != "different note" {
		t.Fatalf("round trip lost data: %#v", decoded)
	}
	again, err := Encode(decoded)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatalf("not canonical: %v", err)
	}
	d1, err := Digest(b)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Digest(decoded)
	if err != nil || d1 != d2 {
		t.Fatal("digest mismatch")
	}
}
func TestStrictRejections(t *testing.T) {
	base := `{"format":"findrail-evidence-bundle","version":1,"title":"x","sources":[{"alias":"s1","kind":"filesystem","display_name":"x"}],"entries":[{"source_alias":"s1","relative_path":"a","media_type":"text/plain","content_hash":"` + strings.Repeat("a", 64) + `","page":0,"start_char":0,"text":"x","note":""}]}`
	cases := []string{
		strings.Replace(base, `"title":"x"`, `"title":"x","title":"y"`, 1),
		strings.Replace(base, `"display_name":"x"`, `"display_name":"x","display_name":"y"`, 1),
		strings.Replace(base, `"note":""`, `"note":"","note":"y"`, 1),
		strings.Replace(base, `"text":"x"`, `"text":"x","extra":true`, 1),
		strings.Replace(base, `"version":1`, `"version":1.0`, 1),
		strings.Replace(base, `"version":1`, `"version":999999999999999999999`, 1),
		strings.Replace(base, `"relative_path":"a"`, `"relative_path":"../a"`, 1),
		strings.Replace(base, `"relative_path":"a"`, `"relative_path":"https://example.test/a"`, 1),
		base + ` {}`, strings.Replace(base, `"source_alias":"s1"`, `"source_alias":"s2"`, 1),
	}
	for i, s := range cases {
		if _, err := Decode(strings.NewReader(s)); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if _, err := Decode(bytes.NewReader([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}
func TestLimitsAndEmptyBundle(t *testing.T) {
	b := Bundle{Title: "empty", Sources: []Source{}, Entries: []Entry{}}
	raw, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	b = sample()
	b.Entries[0].StartChar = 2147483648
	if _, err = Encode(b); err == nil {
		t.Fatal("overflow start accepted")
	}
	b = sample()
	b.Entries[0].RelativePath = strings.Repeat("a", 2049)
	if _, err = Encode(b); err == nil {
		t.Fatal("oversize path accepted")
	}
	b = sample()
	b.Entries[0].Text = strings.Repeat("x", 256<<10+1)
	if _, err = Encode(b); err == nil {
		t.Fatal("oversize excerpt accepted")
	}
	if _, err := Decode(strings.NewReader(strings.Repeat(" ", MaxRaw+1))); err == nil {
		t.Fatal("oversize input accepted")
	}
}
