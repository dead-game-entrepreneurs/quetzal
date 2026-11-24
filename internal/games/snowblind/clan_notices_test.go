package snowblind

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func noticeArgs(text string) []byte {
	return encode(func(w *quazal.Writer) { w.String(text) })
}

func readClanNotices(t *testing.T, s *startup, pid uint32) []clanNotice {
	t.Helper()
	r := quazal.NewReader(clanCall(t, s, pid, 2, nil))
	if r.U32() != 0 {
		t.Fatal("clan lookup failed")
	}
	r.String(3)
	r.String(20)
	r.U32()
	count := r.U32()
	if count > maxClanNotices {
		t.Fatal("unbounded notice list")
	}
	notices := []clanNotice{}
	for range count {
		notices = append(notices, clanNotice{Author: r.String(15), Body: r.String(maxClanNoticeBytes)})
	}
	for range 3 {
		r.Bool()
	}
	r.String(255)
	r.String(255)
	if r.U32() != 1 {
		t.Fatal("notice list corrupted roster")
	}
	for range 2 {
		r.String(15)
		r.String(255)
		for range 3 {
			r.Bool()
		}
		if r.U32() != 0 {
			t.Fatal("unexpected gathering presence")
		}
		r.Bool()
	}
	if !r.Done() {
		t.Fatal("notice reply does not match the native complete schema")
	}
	return notices
}

func TestClanNoticePostPersistsAndMatchesNativeReply(t *testing.T) {
	s, players, path := clanFixture(t)
	pid := players[0].PID
	clanWantStatus(t, clanCall(t, s, pid, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	// Existing version-1 files without notices must load without migration.
	loaded, err := openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	clanWantStatus(t, clanCall(t, s, pid, 17, noticeArgs("Synthetic notice")), 0)
	fixture, err := os.ReadFile("testdata/clans-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Reply string `json:"notice_fetch_my_clan_reply_hex"`
	}
	if err := json.Unmarshal(fixture, &native); err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(native.Reply)
	if err != nil || !bytes.Equal(clanCall(t, s, pid, 2, nil), want) {
		t.Fatal("populated notice reply differs from native field codecs")
	}
	loaded, err = openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	notices := readClanNotices(t, s, pid)
	if len(notices) != 1 || notices[0].Author != players[0].Name || notices[0].Body != "Synthetic notice" {
		t.Fatal("notice lost on reload", notices)
	}
	before, _ := os.ReadFile(path)
	clanWantStatus(t, clanCall(t, s, pid, 18, nil), 0)
	clanWantStatus(t, clanCall(t, s, pid, 18, nil), 0)
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(readClanNotices(t, s, pid)) != 1 {
		t.Fatal("mark-read deleted or changed the notice board")
	}
}

func TestClanNoticesEnforceMembershipTextLimitsAndRetention(t *testing.T) {
	s, players, _ := clanFixture(t)
	pid, other := players[0].PID, players[1].PID
	clanWantStatus(t, clanCall(t, s, pid, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	clanWantStatus(t, clanCall(t, s, other, 17, noticeArgs("Foreign post")), clanNotMember)
	clanWantStatus(t, clanCall(t, s, other, 18, nil), clanNotMember)
	for _, text := range []string{"", "   ", "New\nline", strings.Repeat("x", maxClanNoticeBytes+1)} {
		clanWantStatus(t, clanCall(t, s, pid, 17, noticeArgs(text)), clanIllegalText)
	}
	if len(readClanNotices(t, s, pid)) != 0 {
		t.Fatal("rejected post changed notice board")
	}
	for _, text := range []string{"First", "Second", "Third", strings.Repeat("x", maxClanNoticeBytes)} {
		clanWantStatus(t, clanCall(t, s, pid, 17, noticeArgs(text)), 0)
	}
	notices := readClanNotices(t, s, pid)
	if len(notices) != maxClanNotices || notices[0].Body != strings.Repeat("x", maxClanNoticeBytes) || notices[1].Body != "Third" || notices[2].Body != "Second" {
		t.Fatal("latest three notices were not retained in display order", notices)
	}
	clanWantStatus(t, clanCall(t, s, other, 3, clanCreateArgs("NEW", "Other Clan")), 0)
	clanWantStatus(t, clanCall(t, s, other, 17, noticeArgs("Separate board")), 0)
	if len(readClanNotices(t, s, other)) != 1 || len(readClanNotices(t, s, pid)) != maxClanNotices {
		t.Fatal("notice boards crossed clan boundaries")
	}
}

func TestClanNoticeMalformedRequestAndFailedWriteLeaveBoardUnchanged(t *testing.T) {
	s, players, path := clanFixture(t)
	pid := players[0].PID
	clanWantStatus(t, clanCall(t, s, pid, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	args := noticeArgs("Synthetic notice")
	for n := range len(args) {
		if _, err := s.clans.handle(pid, quazal.Request{Protocol: 102, Method: 17, Args: args[:n]}); err == nil {
			t.Fatalf("accepted notice truncation %d", n)
		}
	}
	for _, request := range []quazal.Request{{Protocol: 102, Method: 17, Args: append(bytes.Clone(args), 0)}, {Protocol: 102, Method: 18, Args: []byte{0}}} {
		if _, err := s.clans.handle(pid, request); err == nil {
			t.Fatal("accepted trailing notice/read arguments")
		}
	}
	before, _ := os.ReadFile(path)
	s.clans.path = filepath.Join(filepath.Dir(path), "missing", "clans.json")
	if _, err := s.clans.handle(pid, quazal.Request{Protocol: 102, Method: 17, Args: args}); err == nil {
		t.Fatal("acknowledged a failed notice commit")
	}
	s.clans.path = path
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(readClanNotices(t, s, pid)) != 0 {
		t.Fatal("failed notice write changed persisted or live board")
	}
}
