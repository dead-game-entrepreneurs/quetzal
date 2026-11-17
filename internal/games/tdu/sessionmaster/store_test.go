package sessionmaster

import (
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func owner(n uint64) Owner {
	return Owner{Remote: netip.MustParseAddrPort("192.0.2.1:8122"), Generation: n}
}
func request(cid uint32, x float32) CreateRequest {
	return CreateRequest{Client: Client{Name: "Driver", ConnectionID: cid, Address: 0xc0000201, Port: 8122}, Session: FreeRide{Identity: uint64(cid), Game: "TestDriveV2.30", Name: "FreeRideV5N37-8max", Capacity: 8, Bounds: Bounds{x - 1500, -1500, x + 1500, 1500}}}
}
func testStore(t *testing.T, opts StoreOptions) *Store {
	t.Helper()
	s, e := NewStore(opts)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func publish(t *testing.T, s *Store, o Owner, d FreeRide) FreeRide {
	t.Helper()
	d.Connection = [4]uint32{8889, 0xc0000202, 8889, uint32(d.Identity)}
	d.Reference = 0x08080808
	event, e := s.Update(o, d)
	if e != nil || event != 0 {
		t.Fatalf("publish %x %v", event, e)
	}
	actual, _ := s.Get(d.ID)
	if actual.Reference != 0xc0000201 {
		t.Fatal("trusted spoofed reference")
	}
	return actual
}
func TestLifecycleMatchingCapacityAndIdempotency(t *testing.T) {
	now := time.Unix(100, 0)
	s := testStore(t, StoreOptions{Now: func() time.Time { return now }})
	q := request(11, 0)
	q.Session.Capacity = 2
	result, host, e := s.Create(owner(1), q)
	if e != nil || result != 1 || host.ID == 0 {
		t.Fatalf("create %d %+v %v", result, host, e)
	}
	result, retry, e := s.Create(owner(1), q)
	if e != nil || result != 1 || retry.ID != host.ID || len(s.Snapshot()) != 1 {
		t.Fatal("retry created another session")
	}
	// A new record has no peer introduction until its owner publishes a port/CID.
	if s.Snapshot()[0].Published {
		t.Fatal("unpublished endpoint advertised")
	}
	host = publish(t, s, owner(1), host)
	result, joined, e := s.Create(owner(2), request(22, 100))
	if e != nil || result != 2 || joined.ID != host.ID || joined.Occupancy != 2 {
		t.Fatalf("join %d %+v %v", result, joined, e)
	}
	result, retry, e = s.Create(owner(2), request(22, 100))
	if e != nil || result != 2 || retry.Occupancy != 2 {
		t.Fatal("join retry consumed another slot")
	}
	result, newGroup, e := s.Create(owner(3), request(33, 200))
	if e != nil || result != 1 || newGroup.ID == host.ID {
		t.Fatalf("full session admitted another player: %d %+v %v", result, newGroup, e)
	}
	if _, _, e = s.Create(owner(4), request(22, 100)); !errors.Is(e, ErrIdentity) {
		t.Fatalf("duplicate claim %v", e)
	}
	before, _ := s.Get(host.ID)
	spoof := host
	spoof.Occupancy = 2
	spoof.Bounds.MinX = -99999
	if event, e := s.Update(owner(99), spoof); e != nil || event != 0xff {
		t.Fatalf("wrong owner update %x %v", event, e)
	}
	after, _ := s.Get(host.ID)
	if after != before {
		t.Fatal("foreign update mutated record")
	}
	if e = s.Delete(owner(99), host.ID); !errors.Is(e, ErrOwnership) {
		t.Fatalf("foreign delete %v", e)
	}
	// Reservation expiry frees capacity, while the live claim stays bound to its
	// owner until explicit movement/disconnect or the host record expires.
	now = now.Add(6 * time.Second)
	if got, _ := s.Get(host.ID); got.Occupancy != 1 {
		t.Fatal("reservation did not expire")
	}
	if _, _, e = s.Create(owner(4), request(22, 100)); !errors.Is(e, ErrIdentity) {
		t.Fatal("expired reservation released live identity claim")
	}
	s.DropOwner(owner(2))
	if s.OwnerConnectionID(owner(2)) != 0 {
		t.Fatal("disconnect retained claim")
	}
	if e = s.Delete(owner(1), host.ID); e != nil {
		t.Fatal(e)
	}
	if _, ok := s.Get(host.ID); ok {
		t.Fatal("delete retained session")
	}
	if e = s.Delete(owner(1), host.ID); e != nil {
		t.Fatal("delete not idempotent")
	}
	now = now.Add(20 * time.Second)
	if len(s.Snapshot()) != 0 {
		t.Fatal("session TTL did not expire")
	}
}
func TestSpatialBlacklistOwnershipAndLimits(t *testing.T) {
	s := testStore(t, StoreOptions{MaxSessions: 2})
	_, a, _ := s.Create(owner(1), request(1, 0))
	a = publish(t, s, owner(1), a)
	q := request(2, 20000)
	result, b, e := s.Create(owner(2), q)
	if e != nil || result != 1 || b.ID == a.ID {
		t.Fatal("matched disjoint bounds")
	}
	q = request(3, 0)
	q.Blacklist = []Exclusion{{a.ID, 10}}
	if _, _, e = s.Create(owner(3), q); !errors.Is(e, ErrCapacity) {
		t.Fatalf("blacklist bypass/capacity %v", e)
	}
	s.DropOwner(owner(1))
	if _, ok := s.Get(a.ID); ok {
		t.Fatal("owner disconnect retained group")
	}
	result, c, e := s.Create(owner(3), request(3, 0))
	if e != nil || result != 1 || c.ID == a.ID {
		t.Fatal("reused expired session ID")
	}
	bad := request(4, 0)
	bad.Client.ConnectionID = 0
	if _, _, e = s.Create(owner(4), bad); !errors.Is(e, ErrMalformed) {
		t.Fatal("missing native connection identity")
	}
	bad = request(3, 0)
	bad.Session.Name = "AnotherMode"
	if result, moved, err := s.Create(owner(3), bad); err != nil || result != 1 || moved.Name != "AnotherMode" {
		t.Fatal("owner could not explicitly select another mode")
	}
	for _, o := range []StoreOptions{{MaxSessions: -1}, {MaxOwners: 32769}, {TTL: -1}, {TTL: time.Second, ReservationTTL: 2 * time.Second}} {
		if _, e := NewStore(o); e == nil {
			t.Fatalf("invalid store options %+v", o)
		}
	}
}
func TestSearchIsBoundedAndPublishedOnly(t *testing.T) {
	s := testStore(t, StoreOptions{})
	for i := uint64(1); i <= 5; i++ {
		_, d, e := s.Create(owner(i), request(uint32(i), float32(i)*10000))
		if e != nil {
			t.Fatal(e)
		}
		if i <= 4 {
			publish(t, s, owner(i), d)
		}
	}
	got := s.Search("FreeRideV5N37-8max", 100)
	if len(got) != 3 || got[0].ID >= got[1].ID {
		t.Fatal("unbounded or unordered search")
	}
	if len(s.Search("unknown", 10)) != 0 || len(s.Search("FreeRideV5N37-8max", 0)) != 0 {
		t.Fatal("wrong search matching")
	}
}

func TestConcurrentJoinReservationsRespectCapacity(t *testing.T) {
	s := testStore(t, StoreOptions{MaxSessions: 1})
	_, d, _ := s.Create(owner(1), request(1, 0))
	publish(t, s, owner(1), d)
	var wg sync.WaitGroup
	joined := make(chan uint64, 32)
	failures := make(chan error, 32)
	for n := uint64(2); n <= 33; n++ {
		wg.Add(1)
		go func(n uint64) {
			defer wg.Done()
			result, got, e := s.Create(owner(n), request(uint32(n), 0))
			if errors.Is(e, ErrCapacity) {
				return
			}
			if e != nil {
				failures <- e
				return
			}
			if result != 2 || got.ID != d.ID {
				failures <- errors.New("concurrent join created wrong group")
				return
			}
			joined <- n
		}(n)
	}
	wg.Wait()
	close(joined)
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	if len(joined) != 7 || s.Snapshot()[0].Occupancy != 8 {
		t.Fatalf("joined%d occupancy%d", len(joined), s.Snapshot()[0].Occupancy)
	}
	for n := range joined {
		result, _, e := s.Create(owner(n), request(uint32(n), 0))
		if e != nil || result != 2 {
			t.Fatal("full group rejected idempotent retry")
		}
	}
}
