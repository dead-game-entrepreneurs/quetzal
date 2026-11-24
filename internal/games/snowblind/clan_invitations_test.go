package snowblind

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func invitationAction(tag, inviter string) []byte {
	return encode(func(w *quazal.Writer) { w.String(tag); w.String(inviter) })
}

func invitationPreference(accept bool) []byte {
	return encode(func(w *quazal.Writer) { w.Bool(accept) })
}

func assertClanAlerts(t *testing.T, s *startup, pid uint32, invite, notices bool) {
	t.Helper()
	r := quazal.NewReader(clanCall(t, s, pid, 16, nil))
	if r.U32() != 0 || r.Bool() != invite || r.Bool() != notices || r.Bool() {
		t.Fatal("wrong clan alert flags")
	}
	if (r.String(3) != "") != invite || r.String(255) != "" || !r.Done() {
		t.Fatal("wrong clan alert metadata")
	}
}

func TestClanInvitationNativeQueryAcceptanceAndPersistence(t *testing.T) {
	s, players, path := clanFixture(t)
	founder, member, outsider := players[0], players[1], players[2]
	clanWantStatus(t, clanCall(t, s, founder.PID, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	clanWantStatus(t, clanCall(t, s, founder.PID, 5, noticeArgs(member.Name)), 0)
	clanWantStatus(t, clanCall(t, s, founder.PID, 5, noticeArgs(member.Name)), 0)
	fixture, err := os.ReadFile("testdata/clans-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Query string `json:"check_my_invitations_reply_hex"`
	}
	if err := json.Unmarshal(fixture, &native); err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(native.Query)
	if err != nil || !bytes.Equal(want, clanCall(t, s, member.PID, 4, nil)) {
		t.Fatal("invitation query differs from native invitation/member codecs")
	}
	r := quazal.NewReader(clanCall(t, s, founder.PID, 4, nil))
	if r.U32() != 0 || r.U32() != 0 || r.U32() != 1 {
		t.Fatal("missing outgoing invitation")
	}
	assertClanAlerts(t, s, member.PID, true, false)
	assertClanAlerts(t, s, founder.PID, false, false)
	assertClanAlerts(t, s, outsider.PID, false, false)
	action := invitationAction("tag", founder.Name)
	clanWantStatus(t, clanCall(t, s, outsider.PID, 7, action), clanInvalidInvitation)
	clanWantStatus(t, clanCall(t, s, member.PID, 9, action), 0)
	assertClanAlerts(t, s, member.PID, false, false)
	if s.clans.memberClan(member.PID) >= 0 || len(s.clans.invitations) != 1 {
		t.Fatal("acknowledging an alert accepted or removed the invitation")
	}
	loaded, err := openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	assertClanAlerts(t, s, member.PID, false, false)
	clanWantStatus(t, clanCall(t, s, member.PID, 7, action), 0)
	loaded, err = openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	if s.clans.abbreviation(member.PID) != "TAG" || len(s.clans.invitations) != 0 {
		t.Fatal("accepted membership did not persist")
	}
	r = quazal.NewReader(clanCall(t, s, member.PID, 2, nil))
	if r.U32() != 0 || r.String(3) != "TAG" || r.String(20) != "Synthetic Clan" || r.U32() != 0xffffffff || r.U32() != 0 {
		t.Fatal("joined member cannot load clan")
	}
	for range 3 {
		if r.Bool() {
			t.Fatal("unexpected alert")
		}
	}
	r.String(255)
	r.String(255)
	if r.U32() != 2 {
		t.Fatal("accepted member missing from roster")
	}
	for i, name := range []string{founder.Name, member.Name, member.Name} {
		if r.String(15) != name || r.String(255) != "" || r.Bool() != (i == 0) || r.Bool() || !r.Bool() || r.U32() != 0 || r.Bool() {
			t.Fatal("wrong roster or own-member flags")
		}
	}
	if !r.Done() {
		t.Fatal("trailing roster fields")
	}
	clanWantStatus(t, clanCall(t, s, member.PID, 3, clanCreateArgs("TAG", "Synthetic Clan")), clanAlreadyMember)
	clanWantStatus(t, clanCall(t, s, member.PID, 5, noticeArgs(outsider.Name)), clanPermission)
	clanWantStatus(t, clanCall(t, s, member.PID, 17, noticeArgs("Unauthorized")), clanPermission)
	clanWantStatus(t, clanCall(t, s, founder.PID, 17, noticeArgs("New notice")), 0)
	assertClanAlerts(t, s, member.PID, false, true)
	assertClanAlerts(t, s, founder.PID, false, false)
	clanWantStatus(t, clanCall(t, s, member.PID, 18, nil), 0)
	assertClanAlerts(t, s, member.PID, false, false)
}

func TestClanInvitationPreferencesCancelRejectAndCompetingOffers(t *testing.T) {
	s, players, path := clanFixture(t)
	a, b, target := players[0], players[1], players[2]
	clanWantStatus(t, clanCall(t, s, a.PID, 3, clanCreateArgs("ONE", "First Clan")), 0)
	clanWantStatus(t, clanCall(t, s, b.PID, 3, clanCreateArgs("TWO", "Second Clan")), 0)
	clanWantStatus(t, clanCall(t, s, target.PID, 10, invitationPreference(false)), 0)
	loaded, err := openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	clanWantStatus(t, clanCall(t, s, a.PID, 5, noticeArgs(target.Name)), clanNotAccepting)
	clanWantStatus(t, clanCall(t, s, target.PID, 10, invitationPreference(true)), 0)
	clanWantStatus(t, clanCall(t, s, a.PID, 5, noticeArgs("Missing")), clanInvalidPlayer)
	clanWantStatus(t, clanCall(t, s, a.PID, 5, noticeArgs(b.Name)), clanAlreadyMember)
	clanWantStatus(t, clanCall(t, s, a.PID, 5, noticeArgs(target.Name)), 0)
	clanWantStatus(t, clanCall(t, s, b.PID, 6, noticeArgs(target.Name)), clanInvalidInvitation)
	clanWantStatus(t, clanCall(t, s, a.PID, 6, noticeArgs(target.Name)), 0)
	assertClanAlerts(t, s, target.PID, false, false)
	clanWantStatus(t, clanCall(t, s, a.PID, 5, noticeArgs(target.Name)), 0)
	clanWantStatus(t, clanCall(t, s, target.PID, 8, invitationAction("ONE", a.Name)), 0)
	assertClanAlerts(t, s, target.PID, false, false)
	clanWantStatus(t, clanCall(t, s, a.PID, 5, noticeArgs(target.Name)), 0)
	clanWantStatus(t, clanCall(t, s, b.PID, 5, noticeArgs(target.Name)), 0)
	clanWantStatus(t, clanCall(t, s, target.PID, 7, invitationAction("TWO", b.Name)), 0)
	if len(s.clans.invitations) != 0 || s.clans.abbreviation(target.PID) != "TWO" {
		t.Fatal("acceptance left competing offers or joined wrong clan")
	}
	if _, err := openClans(path, s.accounts); err != nil {
		t.Fatal(err)
	}
}

func TestClanCreationClearsIncomingOffers(t *testing.T) {
	s, players, path := clanFixture(t)
	clanWantStatus(t, clanCall(t, s, players[0].PID, 3, clanCreateArgs("ONE", "First Clan")), 0)
	clanWantStatus(t, clanCall(t, s, players[0].PID, 5, noticeArgs(players[1].Name)), 0)
	clanWantStatus(t, clanCall(t, s, players[1].PID, 3, clanCreateArgs("TWO", "Second Clan")), 0)
	if len(s.clans.invitations) != 0 {
		t.Fatal("creation left a stale invitation")
	}
	if _, err := openClans(path, s.accounts); err != nil {
		t.Fatal(err)
	}
}

func TestClanInvitationFailedWritesAndMalformedRequests(t *testing.T) {
	s, players, path := clanFixture(t)
	pid, target := players[0].PID, players[1].PID
	clanWantStatus(t, clanCall(t, s, pid, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	for method, args := range map[uint32][]byte{4: nil, 5: noticeArgs(players[1].Name), 6: noticeArgs(players[1].Name), 7: invitationAction("TAG", players[0].Name), 8: invitationAction("TAG", players[0].Name), 9: invitationAction("TAG", players[0].Name), 10: invitationPreference(true)} {
		for n := range len(args) {
			if _, err := s.clans.handle(pid, quazal.Request{Protocol: 102, Method: method, Args: args[:n]}); err == nil {
				t.Fatalf("accepted truncated invitation method %d length %d", method, n)
			}
		}
		if _, err := s.clans.handle(pid, quazal.Request{Protocol: 102, Method: method, Args: append(bytes.Clone(args), 0)}); err == nil {
			t.Fatalf("accepted trailing invitation args method %d", method)
		}
	}
	s.clans.path = filepath.Join(filepath.Dir(path), "missing", "clans.json")
	if _, err := s.clans.handle(pid, quazal.Request{Protocol: 102, Method: 5, Args: noticeArgs(players[1].Name)}); err == nil || len(s.clans.invitations) != 0 {
		t.Fatal("failed invitation commit changed memory")
	}
	s.clans.path = path
	clanWantStatus(t, clanCall(t, s, pid, 5, noticeArgs(players[1].Name)), 0)
	s.clans.path = filepath.Join(filepath.Dir(path), "missing", "clans.json")
	if _, err := s.clans.handle(target, quazal.Request{Protocol: 102, Method: 7, Args: invitationAction("TAG", players[0].Name)}); err == nil || s.clans.memberClan(target) >= 0 || len(s.clans.invitations) != 1 {
		t.Fatal("failed acceptance awarded membership or consumed offer")
	}
	loaded, err := openClans(path, s.accounts)
	if err != nil || loaded.memberClan(target) >= 0 || len(loaded.invitations) != 1 {
		t.Fatal("failed acceptance changed disk", err)
	}
}

func TestClanInvitationStateRejectsInvalidReferences(t *testing.T) {
	s, players, path := clanFixture(t)
	a, b := players[0].PID, players[1].PID
	for _, disk := range []clanDisk{
		{Version: 1, Clans: []clanRecord{{Abbreviation: "TAG", Name: "Clan", FounderPID: a, Members: []uint32{a}}}},
		{Version: 1, Clans: []clanRecord{{Abbreviation: "TAG", Name: "Clan", FounderPID: a, Members: []uint32{b}}, {Abbreviation: "NEW", Name: "Other", FounderPID: b}}},
		{Version: 1, Clans: []clanRecord{{Abbreviation: "TAG", Name: "Clan", FounderPID: a, Unread: []uint32{b}}}},
		{Version: 1, Clans: []clanRecord{}, Invitations: []clanInvitation{{Clan: "TAG", From: a, To: b}}},
		{Version: 1, Clans: []clanRecord{}, Declined: []uint32{999999}},
	} {
		if err := saveState(path, ".clans-*", disk, maxClanStateBytes); err != nil {
			t.Fatal(err)
		}
		if _, err := openClans(path, s.accounts); err == nil {
			t.Fatal("accepted corrupt invitation or membership state")
		}
	}
}
