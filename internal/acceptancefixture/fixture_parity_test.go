//go:build acceptancefixture_python

package acceptancefixture

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPythonByteParity is an opt-in integration check against the Python oracle.
// Run with: go test -tags=acceptancefixture_python ./internal/acceptancefixture
func TestPythonByteParity(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("python3 required for optional oracle parity check:", err)
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
		if !bytes.Equal(got, want) {
			t.Errorf("%s bytes differ", filepath.ToSlash(name))
		}
	}
}
