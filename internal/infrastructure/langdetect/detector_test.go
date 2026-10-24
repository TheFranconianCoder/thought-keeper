package langdetect

import (
	"strings"
	"testing"
)

func TestDetectGermanAndEnglish(t *testing.T) {
	d := Detector{}
	if got := d.Detect("Das ist ein deutscher Text über Nebenläufigkeit und Speicherverwaltung."); got != "de" {
		t.Errorf("German = %q, want de", got)
	}
	if got := d.Detect("This is an English text about concurrency and memory management."); got != "en" {
		t.Errorf("English = %q, want en", got)
	}
	if got := d.Detect("short"); got != "" {
		t.Errorf("short text = %q, want empty", got)
	}
}

func TestDetectLongDocuments(t *testing.T) {
	d := Detector{}
	english := strings.Repeat("This is an English text about concurrency and memory management. ", 500)
	if got := d.Detect(english); got != "en" {
		t.Errorf("long English = %q, want en", got)
	}
	german := strings.Repeat("Das ist ein deutscher Text über Nebenläufigkeit und Speicherverwaltung. ", 500)
	if got := d.Detect(german); got != "de" {
		t.Errorf("long German = %q, want de", got)
	}
}

func TestDetectLooksPastACodeHeavyOpening(t *testing.T) {
	d := Detector{}
	code := strings.Repeat("func main() { fmt.Println(\"hello\") }\n", 100)
	german := strings.Repeat("Das ist ein deutscher Text über Nebenläufigkeit und Speicherverwaltung. ", 300)
	if got := d.Detect(code + german); got != "de" {
		t.Errorf("German after code = %q, want de", got)
	}
}
