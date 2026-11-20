package ttl25

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMOTDLocalesReloadAndRetainLastValidMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motd.json")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"English":"Welcome","fr":"Bonjour","default":"Fallback"}`)
	m, err := loadMessageOfDay(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for locale, want := range map[string]string{"English": "Welcome", "en-US": "Welcome", "FR_fr": "Bonjour", "German": "Fallback"} {
		if got := m.message(locale); got != want {
			t.Fatalf("%s: %q, want %q", locale, got, want)
		}
	}
	write(`{"english":"Updated\nGame night"}`)
	if got := m.message("es"); got != "Updated\nGame night" {
		t.Fatal("live reload/English fallback", got)
	}
	write(`{"english":`)
	if got := m.message("English"); got != "Updated\nGame night" {
		t.Fatal("bad reload erased working message", got)
	}
	if _, err := loadMessageOfDay(path, nil); err == nil {
		t.Fatal("malformed startup config accepted")
	}
	write(`{"en":"A","English":"B"}`)
	if _, err := loadMessageOfDay(path, nil); err == nil {
		t.Fatal("ambiguous locale accepted")
	}
	write(`{"en":"` + strings.Repeat("x", 1025) + `"}`)
	if _, err := loadMessageOfDay(path, nil); err == nil {
		t.Fatal("oversized retail message accepted")
	}
}

func TestCommunityMOTDReturnsSDKString(t *testing.T) {
	f := newCommunityFixture(t)
	b := communityCall(t, f.c, f.pa, f.a.PID, 38, rankingString(nil, "English"))
	r := rankingReader{b: b}
	if r.str() != defaultMOTD || !r.done() {
		t.Fatal("invalid MOTD reply")
	}
}
