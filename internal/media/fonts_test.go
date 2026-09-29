package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFontLicensesAndChecksums(t *testing.T) {
	dir := New(Config{}).cfg.FontDir
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Files map[string]struct{ SHA256 string }
	}
	if err := json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	for name, metadata := range original.Files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		matches := hex.EncodeToString(sum[:]) == metadata.SHA256
		// The upstream manifest mixes LF and CRLF license hashes. Accept only
		// line-ending changes in text licenses; all font binaries remain exact.
		if !matches && strings.HasSuffix(name, "-OFL.txt") {
			lf := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
			for _, canonical := range [][]byte{lf, bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))} {
				sum = sha256.Sum256(canonical)
				matches = matches || hex.EncodeToString(sum[:]) == metadata.SHA256
			}
		}
		if !matches {
			t.Fatalf("copied upstream font/license changed: %s", name)
		}
		if strings.HasSuffix(name, "-OFL.txt") && !strings.Contains(string(data), "SIL OPEN FONT LICENSE") {
			t.Fatalf("font license missing: %s", name)
		}
	}
	var static struct {
		File, SHA256, License string
	}
	data, err = os.ReadFile(filepath.Join(dir, "static-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &static); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(dir, static.File))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != static.SHA256 {
		t.Fatal("static font checksum differs from provenance")
	}
	if _, err := os.Stat(filepath.Join(dir, static.License)); err != nil {
		t.Fatal(err)
	}
}
