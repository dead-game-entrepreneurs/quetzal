package sessionmaster

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"math"
	"os"
	"testing"
)

func fixture(t *testing.T) ([]byte, []byte, []byte) {
	t.Helper()
	b, e := os.ReadFile("testdata/schema.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Free   string `json:"free_hex"`
		Client string `json:"client_hex"`
		Create string `json:"create_hex"`
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	decode := func(s string) []byte {
		b, e := hex.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	return decode(f.Free), decode(f.Client), decode(f.Create)
}
func TestIndependentCodecFixtures(t *testing.T) {
	free, client, create := fixture(t)
	d, e := ParseFreeRide(free)
	if e != nil {
		t.Fatal(e)
	}
	if d.Identity != 0x0102030405060708 || d.ID != 0x11121314 || d.Reference != 0xc0000201 || d.Connection != [4]uint32{8889, 0xc0000202, 8889, 22} || d.Bounds != (Bounds{-1500.5, 100.25, 1500.5, 3100.25}) || d.Extra[127] != 127 {
		t.Fatalf("FreeRide %+v", d)
	}
	encoded, e := d.MarshalBinary()
	if e != nil || !bytes.Equal(encoded, free) {
		t.Fatalf("FreeRide encode %v", e)
	}
	c, e := ParseClient(client)
	if e != nil {
		t.Fatal(e)
	}
	if c.Name != "Driver" || c.ConnectionID != 0x12345678 || c.Address != 0xc0000201 || c.Port != 8122 || len(c.Extension) != 44 || c.Fields != [4]uint32{1, 2, 3, 4} {
		t.Fatalf("client %+v", c)
	}
	encoded, e = c.MarshalBinary()
	if e != nil || !bytes.Equal(encoded, client) {
		t.Fatalf("client encode %v", e)
	}
	emptyName := c
	emptyName.Name = ""
	emptyWire, e := emptyName.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	if decoded, e := ParseClient(emptyWire); e != nil || decoded.Name != "" {
		t.Fatal("native SessionClient reader permits empty display name")
	}

	q, e := ParseCreateRequest(create)
	if e != nil {
		t.Fatal(e)
	}
	if q.Mode != 3 || len(q.Blacklist) != 2 || q.Blacklist[0] != (Exclusion{0x11223344, 2.5}) {
		t.Fatalf("create %+v", q)
	}
	encoded, e = q.MarshalBinary()
	if e != nil || !bytes.Equal(encoded, create) {
		t.Fatalf("create encode %v", e)
	}
}
func TestCodecBoundsAndTruncations(t *testing.T) {
	free, client, create := fixture(t)
	for n := 0; n < len(free); n++ {
		if _, e := ParseFreeRide(free[:n]); e == nil {
			t.Fatalf("accepted FreeRide truncation%d", n)
		}
	}
	for n := 0; n < len(client); n++ {
		if n == len(client)-46 {
			continue
		}
		if _, e := ParseClient(client[:n]); e == nil {
			t.Fatalf("accepted client truncation%d", n)
		}
	}
	for n := 0; n < len(create); n++ {
		if _, e := ParseCreateRequest(create[:n]); e == nil {
			t.Fatalf("accepted create truncation%d", n)
		}
	}
	for _, wire := range [][]byte{append(bytes.Clone(free), 0), append(bytes.Clone(client), 0), append(bytes.Clone(create), 0)} {
		switch len(wire) {
		case len(free) + 1:
			if _, e := ParseFreeRide(wire); e == nil {
				t.Fatal("FreeRide trailing byte")
			}
		case len(client) + 1:
			if _, e := ParseClient(wire); e == nil {
				t.Fatal("client trailing byte")
			}
		default:
			if _, e := ParseCreateRequest(wire); e == nil {
				t.Fatal("create trailing byte")
			}
		}
	}
	d, _ := ParseFreeRide(free)
	for _, change := range []func(*FreeRide){func(d *FreeRide) { d.Capacity = 9 }, func(d *FreeRide) { d.Occupancy = 9 }, func(d *FreeRide) { d.Bounds.MinX = float32(math.NaN()) }, func(d *FreeRide) { d.Bounds.MaxZ = float32(math.Inf(1)) }, func(d *FreeRide) { d.Game = "" }, func(d *FreeRide) { d.Name = "a\x00b" }, func(d *FreeRide) { d.Bounds.MinX = d.Bounds.MaxX + 1 }} {
		bad := d
		change(&bad)
		if _, e := bad.MarshalBinary(); e == nil {
			t.Fatal("accepted invalid descriptor")
		}
	}
	q, _ := ParseCreateRequest(create)
	q.Blacklist = make([]Exclusion, 17)
	if _, e := q.MarshalBinary(); e == nil {
		t.Fatal("accepted oversized blacklist")
	}
}
func TestActionHashes(t *testing.T) {
	for signature, want := range map[string]uint16{
		"coreresquests::r_gettime()":     GetTime,
		"coreresquests::a_gettime(uint)": GetTimeReply,
		"SessionMaster::r_createorjoin_freeride(object edNetSessionClient,object edNetSessionMasterBlackList,object edNetSessionFreeRide)": CreateOrJoin,
		"SessionMaster::a_createorjoin_freeride(byte errcode,[object edNetSessionFreeRide])":                                               CreateOrJoinReply,
		"SessionMaster::r_update_freeride(object edNetSessionFreeRide)":                                                                    Update,
		"SessionMaster::r_GetSessionInfos(uint)":                                                                                           GetInfo,
		"SessionMaster::a_GetSessionInfos(uint,object=NULL)":                                                                               GetInfoReply,
		"SessionMaster::r_Delete(uint)":                                                                                                    Delete,
		"SessionMaster::r_Search(string,uint)":                                                                                             Search,
		"edNetSessionFreeRide::edNetSessionFreeRideV3()":                                                                                   FreeRideTag,
		"edNetSessionClient::edNetSessionClientV2()":                                                                                       SessionClientTag,
		"ednetgamemaster::blacklist([uint gameid,float timeout_relative],...)":                                                             BlacklistTag,
	} {
		h := crc32.ChecksumIEEE([]byte(signature))
		if got := uint16(h ^ (h >> 16)); got != want {
			t.Fatalf("%s hash%x want%x", signature, got, want)
		}
	}
}

func FuzzCodecBounds(f *testing.F) {
	data, e := os.ReadFile("testdata/schema.json")
	if e != nil {
		f.Fatal(e)
	}
	var fixtures struct {
		Free   string `json:"free_hex"`
		Client string `json:"client_hex"`
		Create string `json:"create_hex"`
	}
	if e = json.Unmarshal(data, &fixtures); e != nil {
		f.Fatal(e)
	}
	for _, value := range []string{fixtures.Free, fixtures.Client, fixtures.Create} {
		b, e := hex.DecodeString(value)
		if e != nil {
			f.Fatal(e)
		}
		f.Add(b)
	}
	f.Add([]byte{})
	f.Add([]byte{0xe2, 0xdd, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		if d, e := ParseFreeRide(b); e == nil {
			encoded, e := d.MarshalBinary()
			if e != nil || !bytes.Equal(encoded, b) {
				t.Fatal("FreeRide accepted noncanonical packet")
			}
		}
		if c, e := ParseClient(b); e == nil {
			encoded, e := c.MarshalBinary()
			if e != nil || !bytes.Equal(encoded, b) {
				t.Fatal("client accepted noncanonical packet")
			}
		}
		if q, e := ParseCreateRequest(b); e == nil {
			encoded, e := q.MarshalBinary()
			if e != nil || !bytes.Equal(encoded, b) {
				t.Fatal("create accepted noncanonical packet")
			}
		}
		ParseSearch(b)
	})
}
