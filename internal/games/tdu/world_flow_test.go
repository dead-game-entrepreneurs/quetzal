package tdu

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/presence"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/sessionmaster"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/titledata"
	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func testAccount(t *testing.T, a Addresses, name string, nonce uint32) (uint32, *protocolClient) {
	t.Helper()
	pid, ticket := loginTicket(t, a.Auth, "guest", "h7fyctiuucf", a.Secure.Port())
	guest := secureProtocol(t, a.Secure, pid, ticket, nonce)
	initial := quazalcrypto.InitialKey([]byte("synthetic world password"))
	args := append(sdkString(name), sdkString(hex.EncodeToString(initial[:]))...)
	args = append(args, u32(0x20)...)
	args = append(args, sdkString("unused@example.invalid")...)
	guest.rpc(25, 1, args)
	pid, ticket = loginTicket(t, a.Auth, name, "synthetic world password", a.Secure.Port())
	return pid, secureProtocol(t, a.Secure, pid, ticket, nonce+1)
}
func (c *nativeClient) sendAction(t *testing.T, port uint16, f ednet.Frame) {
	t.Helper()
	frame, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, 10+len(frame))
	binary.BigEndian.PutUint16(plain, 2)
	binary.BigEndian.PutUint16(plain[2:], c.id)
	binary.BigEndian.PutUint16(plain[4:], port)
	ip := c.publicIP.As4()
	copy(plain[6:10], ip[:])
	copy(plain[10:], frame)
	wire, err := c.send.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	c.write(t, wire)
}
func (c *nativeClient) receiveFrame(t *testing.T, port uint16) ednet.Frame {
	t.Helper()
	plain, _, err := c.receive.Open(c.read(t))
	if err != nil || len(plain) < 10 || binary.BigEndian.Uint16(plain[4:]) != port {
		t.Fatalf("native frame %x %v", plain, err)
	}
	f, err := ednet.ParseFrame(plain[10:])
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (c *nativeClient) business(t *testing.T, port uint16, action []byte) []byte {
	t.Helper()
	c.sendAction(t, port, ednet.Frame{Kind: ednet.CallUnreliable, Action: action})
	f := c.receiveFrame(t, port)
	if f.Kind != ednet.CallUnreliable {
		t.Fatalf("wrong native reply envelope%x", f.Kind)
	}
	return f.Action
}
func createdDescriptor(t *testing.T, b []byte, want byte) sessionmaster.FreeRide {
	t.Helper()
	if len(b) < 5 || binary.BigEndian.Uint16(b) != sessionmaster.CreateOrJoinReply || b[2] != want || int(binary.BigEndian.Uint16(b[3:])) != len(b)-5 {
		t.Fatalf("create/join reply%x", b)
	}
	d, err := sessionmaster.ParseFreeRide(b[5:])
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func freeRideRequest(t *testing.T, cid uint32, name string) []byte {
	t.Helper()
	q := sessionmaster.CreateRequest{Client: sessionmaster.Client{Name: name, ConnectionID: cid}, Session: sessionmaster.FreeRide{Identity: uint64(cid) + 0x100000000, Game: "TestDriveV2.30", Name: "FreeRideV5", Capacity: 8, Bounds: sessionmaster.Bounds{MinX: 0, MinZ: 0, MaxX: 3000, MaxZ: 3000}}}
	b, err := q.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func acceptNativeProbe(t *testing.T, host *protocolClient, source *nativeClient) {
	t.Helper()
	var probe quazal.Request
	for attempts := 0; attempts < 8; attempts++ {
		p := host.read()
		if p.Flags&prudp.FlagAck != 0 {
			continue
		}
		if p.Type != prudp.DATA {
			t.Fatal("unexpected probe transport")
		}
		host.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagAck, Sequence: p.Sequence, Fragment: p.Fragment})
		var err error
		probe, err = quazal.ParseRequest(unwrapRPC(t, p.Payload))
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if probe.Protocol != 3 || probe.Method != 2 || len(probe.Args) < 3 {
		t.Fatalf("missing native probe request %+v", probe)
	}
	n := int(binary.LittleEndian.Uint16(probe.Args))
	if n < 1 || n+2 != len(probe.Args) {
		t.Fatal("probe URL framing")
	}
	url := string(probe.Args[2 : 2+n-1])
	nativePort := source.conn.LocalAddr().(*net.UDPAddr).Port
	if !strings.HasPrefix(url, "udp:/") || !strings.Contains(url, fmt.Sprintf("port=%d;", nativePort)) {
		t.Fatalf("probe used wrong native socket %s", url)
	}
	ack, err := quazal.SuccessReply(probe, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagReliable | prudp.FlagNeedsAck | prudp.FlagSize, Sequence: host.sequence, Payload: wrapRPC(ack)})
	host.sequence++
}

func TestTwoDriversDiscoverJoinLookupReportAndDisconnect(t *testing.T) {
	cfg := localConfig(t)
	a := startAdapter(t, cfg)
	hostPID, host := testAccount(t, a, "alpha", 0x12121000)
	joinerPID, joiner := testAccount(t, a, "bravo", 0x34343000)
	hostNative := nativeHandshake(t, a, cfg.PublicIP)
	joinerNative := nativeHandshake(t, a, cfg.PublicIP)
	// The exact startup directory selectors seed SessionMaster, GPS, USERMAP and resources.
	directory := make([]byte, 12)
	binary.BigEndian.PutUint16(directory, ednet.DirectoryRequest)
	for i, id := range []uint16{0xffff, sessionmaster.GetTime, presence.MapRequestID, presence.UpdateID, ednet.OpenRequest} {
		binary.BigEndian.PutUint16(directory[2+i*2:], id)
	}
	rows := hostNative.call(t, cfg.DirectoryPort, 1, directory)
	for i, port := range []uint16{0, cfg.SessionPort, cfg.DensityPort, cfg.UserMapPort, cfg.ResourcePort} {
		if binary.BigEndian.Uint16(rows[2+i*8+6:]) != port {
			t.Fatalf("undiscoverable service slot%d", i)
		}
	}
	now := hostNative.call(t, cfg.SessionPort, 2, binary.BigEndian.AppendUint16(nil, sessionmaster.GetTime))
	if len(now) != 6 || binary.BigEndian.Uint16(now) != sessionmaster.GetTimeReply || binary.BigEndian.Uint32(now[2:]) == 0 {
		t.Fatal("clock service unavailable")
	}
	for i, c := range []*nativeClient{hostNative, joinerNative} {
		pid, name := hostPID, "alpha"
		if i == 1 {
			pid, name = joinerPID, "bravo"
		}
		update, err := presence.MarshalUpdate(presence.Update{UserID: pid, Name: name, ClientType: 1, Position: presence.Position{X: 1000 + float32(i*100), Z: 1000}, CarMask: 1, ExtraIdentity: pid})
		if err != nil {
			t.Fatal(err)
		}
		c.sendAction(t, cfg.UserMapPort, ednet.Frame{Kind: ednet.CallUnreliable, Action: update})
	}
	track, err := presence.MarshalTrackRequest(presence.TrackRequest{UserID: joinerPID, ClientType: 1, Limit: 16, Position: presence.Position{X: 1100, Z: 1000}, CarMask: 1, NATType: 3})
	if err != nil {
		t.Fatal(err)
	}
	nearby := joinerNative.business(t, cfg.DensityPort, track)
	if len(nearby) < 3 || binary.BigEndian.Uint16(nearby) != presence.TrackReplyID || nearby[2] != 1 {
		t.Fatalf("nearby drivers%x", nearby)
	}
	grid := hostNative.business(t, cfg.DensityPort, []byte{byte(presence.MapRequestID >> 8), byte(presence.MapRequestID & 255), 0, 1})
	if len(grid) != 2+2+presence.Cells+4 || binary.BigEndian.Uint32(grid[len(grid)-4:]) != 2 {
		t.Fatal("density grid does not contain both players")
	}
	created := createdDescriptor(t, hostNative.business(t, cfg.SessionPort, freeRideRequest(t, host.connectionID, "alpha")), 1)
	created.Connection = [4]uint32{4444, 0, 0, host.connectionID}
	update, err := sessionmaster.MarshalUpdate(created)
	if err != nil {
		t.Fatal(err)
	}
	published := hostNative.business(t, cfg.SessionPort, update)
	if len(published) < 7 || binary.BigEndian.Uint16(published) != sessionmaster.Notify || published[6] != 0 {
		t.Fatalf("host endpoint not published%x", published)
	}
	joined := createdDescriptor(t, joinerNative.business(t, cfg.SessionPort, freeRideRequest(t, joiner.connectionID, "bravo")), 2)
	if joined.ID != created.ID || joined.Occupancy != 2 || joined.Connection[0] != 4444 {
		t.Fatal("joined a different/inconsistent session")
	}
	joinerNative.sendAction(t, cfg.SessionPort, ednet.Frame{Kind: ednet.Call, RequestID: 55, Timeout: 1000, Action: sessionmaster.MarshalID(sessionmaster.GetInfo, created.ID)})
	business := joinerNative.receiveFrame(t, cfg.SessionPort)
	complete := joinerNative.receiveFrame(t, cfg.SessionPort)
	if complete.Kind != ednet.Reply || complete.RequestID != 55 || len(complete.Action) != 0 || business.Kind != ednet.CallUnreliable || binary.BigEndian.Uint16(business.Action) != sessionmaster.GetInfoReply {
		t.Fatal("get-info reply/completion not separated")
	}
	lookup := joiner.rpc(11, 2, append(u32(0), u32(hostPID)...))
	if len(lookup.Payload) < 11 || lookup.Payload[0] != 1 || binary.LittleEndian.Uint32(lookup.Payload[1:]) != 1 {
		t.Fatalf("native connection lookup%x", lookup.Payload)
	}
	n := int(binary.LittleEndian.Uint16(lookup.Payload[5:]))
	if n < 1 || 7+n+4 != len(lookup.Payload) {
		t.Fatal("connection row framing")
	}
	url := string(lookup.Payload[7 : 7+n-1])
	if !strings.Contains(url, "port=4444;") || !strings.Contains(url, fmt.Sprintf("PID=%d;", hostPID)) {
		t.Fatalf("wrong peer endpoint %q", url)
	}
	if got := joiner.rpc(3, 1, append(u32(1), sdkString(url)...)); len(got.Payload) != 0 {
		t.Fatal("NAT initiation reply schema")
	}
	// The authenticated host receives a server request naming the caller's native
	// engine source port, rather than the caller's separate backend-RMC port.
	acceptNativeProbe(t, host, joinerNative)
	stats, _ := titledata.GameStats{Name: "alpha", Values: [3]uint32{123, 7, 0}}.MarshalBinary()
	if !bytes.Equal(host.rpc(103, 1, stats).Payload, []byte{1}) {
		t.Fatal("report not persisted")
	}
	query := joiner.rpc(103, 2, append(append(u32(2), u32(0)...), u32(20)...))
	if binary.LittleEndian.Uint32(query.Payload) != 1 {
		t.Fatal("reported game data unavailable")
	}
	initial := append(sdkString("challenge"), sdkString("test")...)
	initial = append(initial, sdkString("alpha")...)
	initial = append(u32(7), initial...)
	challenge := []byte("synthetic world challenge")
	initial = append(initial, u32(uint32(len(challenge)))...)
	initial = append(initial, challenge...)
	fileID := binary.LittleEndian.Uint32(host.rpc(102, 1, initial).Payload)
	request := binary.LittleEndian.AppendUint64(u32(7), uint64(fileID))
	download := joiner.rpc(102, 2, request)
	if download.Payload[0] != 1 {
		t.Fatal("challenge download failed")
	}
	data, rest := takeBuffer(t, download.Payload[1:])
	if !bytes.Equal(data, challenge) || len(rest) != 0 {
		t.Fatal("challenge bytes changed")
	}
	closeWire, err := hostNative.send.Seal(nativeControl(hostNative.id, 64, nil))
	if err != nil {
		t.Fatal(err)
	}
	hostNative.write(t, closeWire)
	removed := false
	for attempt := 0; attempt < 20; attempt++ {
		joinerNative.sendAction(t, cfg.SessionPort, ednet.Frame{Kind: ednet.CallUnreliable, Action: sessionmaster.MarshalID(sessionmaster.GetInfo, created.ID)})
		f := joinerNative.receiveFrame(t, cfg.SessionPort)
		if len(f.Action) == 6 && binary.BigEndian.Uint32(f.Action[2:]) == 0 {
			removed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !removed {
		t.Fatal("host departure left stale session")
	}
	grid = joinerNative.business(t, cfg.DensityPort, []byte{byte(presence.MapRequestID >> 8), byte(presence.MapRequestID & 255), 0, 1})
	if binary.BigEndian.Uint32(grid[len(grid)-4:]) != 1 {
		t.Fatal("departed host remained on density map")
	}
}

// This verifies backend race registration and transitions. It does not simulate
// peer race start, driving, or finish synchronization performed by retail clients.
func TestTwoDriversHostBrowseJoinAndLeaveDriveIn(t *testing.T) {
	cfg := localConfig(t)
	a := startAdapter(t, cfg)
	hostPID, host := testAccount(t, a, "racehost", 0x56565000)
	joinerPID, joiner := testAccount(t, a, "racejoin", 0x78787000)
	hostNative := nativeHandshake(t, a, cfg.PublicIP)
	joinerNative := nativeHandshake(t, a, cfg.PublicIP)
	cruise := createdDescriptor(t, hostNative.business(t, cfg.SessionPort, freeRideRequest(t, host.connectionID, "racehost")), 1)
	base := sessionmaster.BaseSession{Identity: 0x1122334455667788, Game: "TestDriveV2.30", Name: presence.DriveInSessionName, Capacity: 4, Connection: [4]uint32{4444, 0, 0, host.connectionID}}
	request, err := (sessionmaster.GenericRequest{Client: sessionmaster.Client{Name: "racehost", ConnectionID: host.connectionID}, Session: base}).MarshalBinary(sessionmaster.GenericCreate)
	if err != nil {
		t.Fatal(err)
	}
	created := hostNative.call(t, cfg.SessionPort, 200, request)
	code, result, id, objects, err := sessionmaster.ParseGenericResult(created)
	if err != nil || code != sessionmaster.GenericCreateReply || result != 1 || id == cruise.ID || len(objects) != 1 {
		t.Fatalf("race creation %x: %v", created, err)
	}
	actual, err := sessionmaster.ParseBaseSession(objects[0])
	if err != nil || actual.Identity != base.Identity || actual.Connection[3] != host.connectionID || actual.Name != base.Name {
		t.Fatal("race descriptor changed", actual, err)
	}
	// Retransmission must return the same record and never inflate activity counts.
	retry := hostNative.call(t, cfg.SessionPort, 201, request)
	_, result, retryID, _, err := sessionmaster.ParseGenericResult(retry)
	if err != nil || result != 1 || retryID != id {
		t.Fatal("race creation retry duplicated record", err)
	}
	density := joinerNative.business(t, cfg.SessionPort, sessionmaster.MarshalID(sessionmaster.SessionDensity, 2))
	if len(density) != 12+presence.SessionDensityCells || binary.BigEndian.Uint16(density) != sessionmaster.SessionDensityReply || binary.BigEndian.Uint32(density[6:]) != 1 || density[12+presence.AggregateDensityIndex] != 1 {
		t.Fatalf("Drive-In density %x", density)
	}
	// A cruising record replaced by a race record must not remain discoverable.
	missing := joinerNative.business(t, cfg.SessionPort, sessionmaster.MarshalID(sessionmaster.GetInfo, cruise.ID))
	if len(missing) != 6 || binary.BigEndian.Uint32(missing[2:]) != 0 {
		t.Fatal("abandoned cruising record survived race creation")
	}
	restrictions, err := (sessionmaster.RestrictionUpdate{SessionID: id, Capacity: 3, Restrictions: sessionmaster.Restrictions{Signed: -1, ID: 7, Category: 2}}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if complete := hostNative.call(t, cfg.SessionPort, 202, restrictions); len(complete) != 0 {
		t.Fatal("restriction update invented a business response")
	}
	update, err := presence.MarshalUpdate(presence.Update{UserID: joinerPID, Name: "racejoin", ClientType: 2, Position: presence.Position{X: 1000, Z: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	joinerNative.sendAction(t, cfg.UserMapPort, ednet.Frame{Kind: ednet.CallUnreliable, Action: update})
	// Knowing an account/CID before discovery does not permit an introduction.
	if reply := joiner.exchange(11, 2, append(u32(host.connectionID), u32(0)...)); reply.Success {
		t.Fatal("pre-discovery peer lookup was admitted")
	}
	search, err := (sessionmaster.FilterQuery{Name: base.Name, Limit: 20, Signed: -1, ID: 7, Category: 2}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	joinerNative.sendAction(t, cfg.SessionPort, ednet.Frame{Kind: ednet.Call, RequestID: 203, Timeout: 1000, Action: search})
	row, complete := joinerNative.receiveFrame(t, cfg.SessionPort), joinerNative.receiveFrame(t, cfg.SessionPort)
	if row.Kind != ednet.CallUnreliable || len(row.Action) < 8 || binary.BigEndian.Uint16(row.Action) != sessionmaster.SearchReply || binary.BigEndian.Uint32(row.Action[2:]) != id || complete.Kind != ednet.Reply || complete.RequestID != 203 || len(complete.Action) != 0 {
		t.Fatal("filtered browser response/completion mismatch")
	}
	n := int(binary.BigEndian.Uint16(row.Action[6:]))
	if n != len(row.Action)-8 {
		t.Fatal("browser descriptor framing")
	}
	listed, err := sessionmaster.ParseBaseSession(row.Action[8:])
	if err != nil || listed.Capacity != 3 || listed.Fields[3] != 7 || listed.Fields[4] != 2 {
		t.Fatal("browser restrictions missing", listed, err)
	}
	// Exact stock browser form: udp scheme, address/port and RVCID, no account PID.
	browserURL := fmt.Sprintf("udp:/address=127.0.0.1;port=4444;RVCID=%d", host.connectionID)
	if reply := joiner.rpc(3, 1, append(u32(1), sdkString(browserURL)...)); len(reply.Payload) != 0 {
		t.Fatal("browser prejoin probe schema")
	}
	acceptNativeProbe(t, host, joinerNative)
	if reply := joiner.rpc(11, 2, append(u32(host.connectionID), u32(0)...)); len(reply.Payload) < 5 || binary.LittleEndian.Uint32(reply.Payload[1:]) != 1 {
		t.Fatal("discovered prejoin host lookup failed")
	}
	criteria := sessionmaster.BaseSession{ID: id, Game: base.Game, Name: base.Name, Capacity: 3, Fields: [5]uint32{0, 0, 0, 7, 2}}
	request, err = (sessionmaster.GenericRequest{Client: sessionmaster.Client{Name: "racejoin", ConnectionID: joiner.connectionID}, Session: criteria}).MarshalBinary(sessionmaster.GenericJoin)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinerNative.call(t, cfg.SessionPort, 204, request)
	code, result, joinedID, objects, err := sessionmaster.ParseGenericResult(joined)
	if err != nil || code != sessionmaster.GenericJoinReply || result != 1 || joinedID != id || len(objects) != 2 {
		t.Fatalf("race join %x: %v", joined, err)
	}
	if lookup := joiner.rpc(11, 2, append(u32(0), u32(hostPID)...)); len(lookup.Payload) < 5 || lookup.Payload[0] != 1 || binary.LittleEndian.Uint32(lookup.Payload[1:]) != 1 {
		t.Fatal("race member cannot resolve native host")
	}
	// Deletion releases both owners' race claims and the Drive-In count.
	if deleted := hostNative.call(t, cfg.SessionPort, 205, sessionmaster.MarshalID(sessionmaster.Delete, id)); len(deleted) != 0 {
		t.Fatal("delete response schema")
	}
	density = joinerNative.business(t, cfg.SessionPort, sessionmaster.MarshalID(sessionmaster.SessionDensity, 2))
	if len(density) != 10 || binary.BigEndian.Uint32(density[6:]) != 0 {
		t.Fatal("race deletion left activity behind")
	}
	if lookup := joiner.exchange(11, 2, append(u32(0), u32(hostPID)...)); lookup.Success {
		t.Fatal("departed race retained peer introduction")
	}
	returned := createdDescriptor(t, hostNative.business(t, cfg.SessionPort, freeRideRequest(t, host.connectionID, "racehost")), 1)
	if returned.ID == id || returned.ID == cruise.ID {
		t.Fatal("return to cruising reused removed session ID")
	}
	// Keep the verified host registration alive through all transitions.
	if len(host.rpc(104, 1, nil).Payload) != 4 {
		t.Fatal("host secure registration lost during native transition")
	}
}
