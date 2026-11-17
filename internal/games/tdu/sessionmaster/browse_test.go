package sessionmaster

import (
	"errors"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
)

func TestBrowseIntroductionIsExactShortLivedDiscoveryWithoutMembership(t *testing.T) {
	now := time.Unix(100, 0)
	store := testStore(t, StoreOptions{Now: func() time.Time { return now }})
	svc, _ := New(Options{Port: 9002, Store: store})
	q := genericRequest(2222, "DriveIn_PS2")
	q.Session.Identity = 900001
	host, e := store.CreateGeneric(owner(1), q)
	if e != nil {
		t.Fatal(e)
	}
	caller := owner(2)
	if _, ok := svc.BrowseIntroduction(caller, 2222); ok {
		t.Fatal("unlisted host introduced")
	}
	query := FilterQuery{Name: q.Session.Name, Limit: 3, Signed: -1}
	wire, _ := query.MarshalBinary()
	frames := call(t, svc, ednet.Call, 10, info(2), wire)
	if len(frames) != 2 {
		t.Fatal("missing exact listed host")
	}
	intro, ok := svc.BrowseIntroduction(caller, 2222)
	if !ok || intro.CallerOwner != caller || intro.TargetOwner != owner(1) || intro.SessionID != host.ID || intro.TargetHost == nil || intro.TargetHost.Identity != 900001 {
		t.Fatal("missing listed-host capability")
	}
	if store.OwnerConnectionID(caller) != 0 {
		t.Fatal("browsing created a caller CID claim")
	}
	if _, ok := store.Membership(1111, 2222); ok {
		t.Fatal("browsing granted room membership")
	}
	if _, ok := svc.BrowseIntroduction(owner(99), 2222); ok {
		t.Fatal("another generation reused listing")
	}
	if _, ok := svc.BrowseIntroduction(caller, 3333); ok {
		t.Fatal("unlisted CID introduced")
	}
	// Changing the contact behind the same ID invalidates the previously listed endpoint.
	d := host.BaseSession()
	d.Connection[0] = 9999
	if event, e := store.UpdateGeneric(owner(1), d); e != nil || event != 0 {
		t.Fatal(e)
	}
	if _, ok := svc.BrowseIntroduction(caller, 2222); ok {
		t.Fatal("changed endpoint accepted stale listing")
	}
	call(t, svc, ednet.Call, 11, info(2), wire)
	if got, ok := svc.BrowseIntroduction(caller, 2222); !ok || got.TargetHost.Connection[0] != 9999 {
		t.Fatal("fresh listing missing new contact")
	}
	d.Fields[3] = 321
	if event, e := store.UpdateGeneric(owner(1), d); e != nil || event != 0 {
		t.Fatal(e)
	}
	if _, ok := svc.BrowseIntroduction(caller, 2222); ok {
		t.Fatal("changed race classification accepted stale listing")
	}
	call(t, svc, ednet.Call, 14, info(2), wire)
	now = now.Add(10 * time.Second)
	if _, ok := svc.BrowseIntroduction(caller, 2222); ok {
		t.Fatal("expired browse capability")
	}
	call(t, svc, ednet.Call, 12, info(2), wire)
	svc.DropOwner(caller)
	if _, ok := svc.BrowseIntroduction(caller, 2222); ok {
		t.Fatal("caller disconnect retained listing")
	}
	call(t, svc, ednet.Call, 13, info(2), wire)
	if e := store.Delete(owner(1), host.ID); e != nil {
		t.Fatal(e)
	}
	if _, ok := svc.BrowseIntroduction(caller, 2222); ok {
		t.Fatal("deleted record retained introduction")
	}
}
func TestBrowseStateIsBoundedAndExpiryReclaimsIt(t *testing.T) {
	now := time.Unix(100, 0)
	store := testStore(t, StoreOptions{MaxOwners: 1, Now: func() time.Time { return now }})
	store.CreateGeneric(owner(1), genericRequest(22, "DriveIn_PS2"))
	rows := store.Filter(owner(2), FilterQuery{Name: "DriveIn_PS2", Limit: 3, Signed: -1})
	if e := store.RememberBrowse(owner(2), rows); e != nil {
		t.Fatal(e)
	}
	if e := store.RememberBrowse(owner(3), rows); !errors.Is(e, ErrCapacity) {
		t.Fatal("unbounded browsing owners", e)
	}
	now = now.Add(10 * time.Second)
	if e := store.RememberBrowse(owner(3), rows); e != nil {
		t.Fatal("expired listing did not release capacity", e)
	}
	many := testStore(t, StoreOptions{MaxSessions: 64, MaxOwners: 64})
	for cid := uint32(1); cid <= 33; cid++ {
		r, e := many.CreateGeneric(owner(uint64(cid)), genericRequest(cid, "DriveIn_PS2"))
		if e != nil {
			t.Fatal(e)
		}
		if e = many.RememberBrowse(owner(99), []Snapshot{r}); e != nil {
			t.Fatal(e)
		}
	}
	if len(many.browse[owner(99)]) != 32 {
		t.Fatal("unbounded browse targets")
	}
	if _, ok := many.BrowseIntroduction(owner(99), 1); ok {
		t.Fatal("oldest bounded listing not evicted")
	}
	if _, ok := many.BrowseIntroduction(owner(99), 33); !ok {
		t.Fatal("latest listing missing")
	}
}
func TestProfileIdentityAndNativeConnectionIDAreIndependent(t *testing.T) {
	store := testStore(t, StoreOptions{})
	q := request(7001, 0)
	q.Session.Identity = 0x123456789abcdef0
	result, host, e := store.Create(owner(1), q)
	if e != nil || result != 1 || host.Identity != q.Session.Identity || store.OwnerConnectionID(owner(1)) != 7001 {
		t.Fatal("equated native CID with profile identity", e)
	}
	host.Connection = [4]uint32{8889, 0xc0000202, 8889, 7001}
	if event, e := store.Update(owner(1), host); e != nil || event != 0 {
		t.Fatal("CID-consistent host publication rejected", e)
	}
	if got := store.Snapshot()[0]; got.ClaimedConnectionID != 7001 || !got.Published {
		t.Fatal("published native CID missing")
	}
}
