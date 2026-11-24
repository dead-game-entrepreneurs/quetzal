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

func officerFixture(t *testing.T) (*startup, []quazal.Account, string) {
	t.Helper()
	s, players, path := clanFixture(t)
	clanWantStatus(t, clanCall(t, s, players[0].PID, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	clanWantStatus(t, clanCall(t, s, players[0].PID, 5, noticeArgs(players[1].Name)), 0)
	clanWantStatus(t, clanCall(t, s, players[1].PID, 7, invitationAction("TAG", players[0].Name)), 0)
	return s, players, path
}

func TestClanPromotionDemotionNativeOfficerReplyAndPersistence(t *testing.T) {
	s, players, path := officerFixture(t)
	leader, member := players[0], players[1]
	clanWantStatus(t, clanCall(t, s, leader.PID, 13, noticeArgs(member.Name)), 0)
	b, err := os.ReadFile("testdata/clans-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Reply string `json:"promoted_fetch_my_clan_reply_hex"`
	}
	if err := json.Unmarshal(b, &native); err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(native.Reply)
	if err != nil || !bytes.Equal(want, clanCall(t, s, member.PID, 2, nil)) {
		t.Fatal("promoted roster/own-member flags differ from native member writer")
	}
	before, _ := os.ReadFile(path)
	clanWantStatus(t, clanCall(t, s, leader.PID, 13, noticeArgs("sEcOnD")), 0)
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(s.clans.records[0].Officers) != 1 {
		t.Fatal("promotion retry duplicated role")
	}
	loaded, err := openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	if !bytes.Equal(want, clanCall(t, s, member.PID, 2, nil)) {
		t.Fatal("officer role did not survive reload")
	}
	clanWantStatus(t, clanCall(t, s, leader.PID, 14, noticeArgs(member.Name)), 0)
	clanWantStatus(t, clanCall(t, s, leader.PID, 14, noticeArgs(member.Name)), 0)
	loaded, err = openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	if len(s.clans.records[0].Officers) != 0 || s.clans.records[0].FounderPID != leader.PID || !clanHasMember(s.clans.records[0], member.PID) {
		t.Fatal("demotion changed membership or leader")
	}
	clanWantStatus(t, clanCall(t, s, member.PID, 17, noticeArgs("Denied after demotion")), clanPermission)
}

func TestClanRolesRequireLeaderAndSameClanTarget(t *testing.T) {
	s, players, _ := officerFixture(t)
	leader, member, outsider := players[0], players[1], players[2]
	for _, method := range []uint32{13, 14} {
		clanWantStatus(t, clanCall(t, s, member.PID, method, noticeArgs(leader.Name)), clanPermission)
		clanWantStatus(t, clanCall(t, s, outsider.PID, method, noticeArgs(member.Name)), clanNotMember)
		clanWantStatus(t, clanCall(t, s, leader.PID, method, noticeArgs(outsider.Name)), clanInvalidPlayer)
		clanWantStatus(t, clanCall(t, s, leader.PID, method, noticeArgs("Missing")), clanInvalidPlayer)
		clanWantStatus(t, clanCall(t, s, leader.PID, method, noticeArgs(leader.Name)), clanPermission)
	}
	clanWantStatus(t, clanCall(t, s, leader.PID, 13, noticeArgs(member.Name)), 0)
	clanWantStatus(t, clanCall(t, s, member.PID, 13, noticeArgs(outsider.Name)), clanPermission)
	clanWantStatus(t, clanCall(t, s, member.PID, 14, noticeArgs(member.Name)), clanPermission)
	clanWantStatus(t, clanCall(t, s, outsider.PID, 3, clanCreateArgs("NEW", "Other Clan")), 0)
	clanWantStatus(t, clanCall(t, s, leader.PID, 13, noticeArgs(outsider.Name)), clanInvalidPlayer)
	for _, pid := range []uint32{quazal.GuestPID, 999999} {
		if _, err := s.clans.handle(pid, quazal.Request{Protocol: 102, Method: 13, Args: noticeArgs(member.Name)}); err == nil {
			t.Fatal("unknown account changed role")
		}
	}
}

func TestClanOfficerCanRecruitCancelOwnInvitationAndPostNotices(t *testing.T) {
	s, players, path := officerFixture(t)
	leader, officer, target := players[0], players[1], players[2]
	clanWantStatus(t, clanCall(t, s, leader.PID, 13, noticeArgs(officer.Name)), 0)
	clanWantStatus(t, clanCall(t, s, officer.PID, 17, noticeArgs("Officer notice")), 0)
	assertClanAlerts(t, s, leader.PID, false, true)
	assertClanAlerts(t, s, officer.PID, false, false)
	if board := s.clans.records[0].Notices; len(board) != 1 || board[0].Author != officer.Name {
		t.Fatal("officer notice author incorrect")
	}
	clanWantStatus(t, clanCall(t, s, leader.PID, 5, noticeArgs(target.Name)), 0)
	clanWantStatus(t, clanCall(t, s, officer.PID, 5, noticeArgs(target.Name)), clanAlreadyInvited)
	clanWantStatus(t, clanCall(t, s, officer.PID, 6, noticeArgs(target.Name)), clanPermission)
	clanWantStatus(t, clanCall(t, s, leader.PID, 6, noticeArgs(target.Name)), 0)
	clanWantStatus(t, clanCall(t, s, officer.PID, 5, noticeArgs(target.Name)), 0)
	clanWantStatus(t, clanCall(t, s, officer.PID, 6, noticeArgs(target.Name)), 0)
	clanWantStatus(t, clanCall(t, s, officer.PID, 5, noticeArgs(target.Name)), 0)
	loaded, err := openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	if len(s.clans.invitations) != 1 || s.clans.invitations[0].From != officer.PID {
		t.Fatal("officer invitation failed reload")
	}
	clanWantStatus(t, clanCall(t, s, leader.PID, 14, noticeArgs(officer.Name)), 0)
	clanWantStatus(t, clanCall(t, s, officer.PID, 5, noticeArgs(target.Name)), clanPermission)
	clanWantStatus(t, clanCall(t, s, officer.PID, 6, noticeArgs(target.Name)), clanPermission)
	clanWantStatus(t, clanCall(t, s, officer.PID, 17, noticeArgs("Denied")), clanPermission)
	// An offer issued while authorized stays valid after the inviter's demotion.
	loaded, err = openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	clanWantStatus(t, clanCall(t, s, target.PID, 7, invitationAction("TAG", officer.Name)), 0)
	if s.clans.abbreviation(target.PID) != "TAG" {
		t.Fatal("valid officer-issued invitation could not be accepted")
	}
}

func TestClanRoleMalformedInputAndFailedSaveAreAtomic(t *testing.T) {
	s, players, path := officerFixture(t)
	leader, member := players[0], players[1]
	args := noticeArgs(member.Name)
	for _, method := range []uint32{13, 14} {
		for n := range len(args) {
			if _, err := s.clans.handle(leader.PID, quazal.Request{Protocol: 102, Method: method, Args: args[:n]}); err == nil {
				t.Fatalf("accepted method %d truncation %d", method, n)
			}
		}
		if _, err := s.clans.handle(leader.PID, quazal.Request{Protocol: 102, Method: method, Args: append(bytes.Clone(args), 0)}); err == nil {
			t.Fatal("role trailing fields")
		}
	}
	before, _ := os.ReadFile(path)
	s.clans.path = filepath.Join(filepath.Dir(path), "missing", "clans.json")
	if _, err := s.clans.handle(leader.PID, quazal.Request{Protocol: 102, Method: 13, Args: args}); err == nil || len(s.clans.records[0].Officers) != 0 {
		t.Fatal("failed promotion published role")
	}
	s.clans.path = path
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed promotion changed disk")
	}
	clanWantStatus(t, clanCall(t, s, leader.PID, 13, args), 0)
	before, _ = os.ReadFile(path)
	s.clans.path = filepath.Join(filepath.Dir(path), "missing", "clans.json")
	if _, err := s.clans.handle(leader.PID, quazal.Request{Protocol: 102, Method: 14, Args: args}); err == nil || !containsPID(s.clans.records[0].Officers, member.PID) {
		t.Fatal("failed demotion removed role")
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed demotion changed disk")
	}
}

func TestClanRoleStateRejectsInvalidOfficers(t *testing.T) {
	s, players, path := officerFixture(t)
	for _, officers := range [][]uint32{{players[0].PID}, {players[2].PID}, {players[1].PID, players[1].PID}, {999999}} {
		bad := append([]clanRecord{}, s.clans.records...)
		bad[0].Officers = officers
		if err := s.clans.save(bad); err != nil {
			t.Fatal(err)
		}
		if _, err := openClans(path, s.accounts); err == nil {
			t.Fatal("invalid officer state accepted")
		}
	}
}
