package acceptancefixture

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestFilesAreDeterministicAndBounded(t *testing.T) {
	first, second := Files(), Files()
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("file count: got %d and %d", len(first), len(second))
	}
	for name, data := range first {
		if len(data) == 0 || len(data) > maxFixtureBytes {
			t.Fatalf("%s size %d outside bound", name, len(data))
		}
		if !bytes.Equal(data, second[name]) {
			t.Fatalf("%s changed between calls", name)
		}
	}
	first["fixture/Juniper-Notes.md"][0] ^= 0xff
	if bytes.Equal(first["fixture/Juniper-Notes.md"], Files()["fixture/Juniper-Notes.md"]) {
		t.Fatal("Files returned shared mutable storage")
	}
}

func TestPythonStructuralParity(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("python3 required for compatibility check:", err)
	}
	script := `import base64, json, pathlib, sys
sys.path.insert(0, str(pathlib.Path("../../scripts").resolve()))
import acceptance_fixture
print(json.dumps({name: base64.b64encode(data.encode("utf-8") if isinstance(data, str) else data).decode("ascii") for name, data in acceptance_fixture.files().items()}, ensure_ascii=False))`
	output, err := exec.Command(python, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("Python fixture generator failed: %v: %s", err, output)
	}
	var expected map[string]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatalf("decode Python output: %v", err)
	}
	actual := Files()
	if len(expected) != len(actual) {
		t.Fatalf("file count differs: Python=%d Go=%d", len(expected), len(actual))
	}
	for name, encoded := range expected {
		want, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := actual[name]
		if !ok {
			t.Fatalf("Go missing %q", name)
		}
		switch filepath.Ext(name) {
		case ".md", ".pdf":
			if !bytes.Equal(got, want) {
				t.Errorf("%s bytes differ", name)
			}
		case ".docx":
			assertZipStructure(t, name, got, want)
		default:
			t.Errorf("unexpected fixture path %q", name)
		}
	}
}

func assertZipStructure(t *testing.T, name string, got, want []byte) {
	t.Helper()
	open := func(label string, data []byte) map[string][]byte {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("%s %s ZIP: %v", name, label, err)
		}
		result := make(map[string][]byte)
		for _, member := range reader.File {
			if member.Name == "" || member.FileInfo().IsDir() || member.Mode().Perm() != 0644 || member.Method != zip.Deflate {
				t.Errorf("%s %s unexpected ZIP metadata for %q: mode=%v method=%d", name, label, member.Name, member.Mode(), member.Method)
			}
			date := member.Modified.UTC()
			if !date.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("%s %s timestamp for %q: %v", name, label, member.Name, date)
			}
			stream, err := member.Open()
			if err != nil {
				t.Fatalf("%s %s member %q: %v", name, label, member.Name, err)
			}
			var content bytes.Buffer
			_, err = content.ReadFrom(stream)
			_ = stream.Close()
			if err != nil {
				t.Fatalf("%s %s member %q: %v", name, label, member.Name, err)
			}
			result[member.Name] = content.Bytes()
		}
		return result
	}
	gotEntries, wantEntries := open("Go", got), open("Python", want)
	if !reflect.DeepEqual(gotEntries, wantEntries) {
		t.Errorf("%s decompressed ZIP entries differ: Go=%v Python=%v", name, keys(gotEntries), keys(wantEntries))
	}
	if len(gotEntries) != 3 {
		t.Errorf("%s has %d entries, want 3", name, len(gotEntries))
	}
}

func keys(values map[string][]byte) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}
