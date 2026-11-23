package ttl25

import (
	"context"
	"log/slog"
	"net"
	"net/netip"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
)

type relayIdentity struct {
	owner sessionOwner
	peer  prudp.Peer
	cid   uint32
	url   string
}
type nativeRelay struct {
	core *peerrelay.Relay
	cfg  RelayConfig
}

func newNativeRelay(cfg RelayConfig, log *slog.Logger) (*nativeRelay, error) {
	return newNativeRelayWithListener(cfg, log, nil)
}
func newNativeRelayWithListener(cfg RelayConfig, log *slog.Logger, listen func(uint16) (*net.UDPConn, error)) (*nativeRelay, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	core, err := peerrelay.New(peerrelay.Options{Config: peerrelay.Config{Mode: cfg.Mode, Bind: cfg.Bind, PublicIP: cfg.PublicIP, FirstPort: cfg.FirstPort, LastPort: cfg.LastPort, ProbeCandidates: true}, Log: log, Listen: listen})
	if err != nil {
		return nil, err
	}
	return &nativeRelay{core: core, cfg: cfg}, nil
}
func relayOwner(o sessionOwner) peerrelay.Owner {
	return peerrelay.Owner{LocalPort: o.port, PeerID: o.peer, PID: o.pid}
}
func (i relayIdentity) shared() peerrelay.Identity {
	return peerrelay.Identity{Owner: relayOwner(i.owner), Peer: i.peer, CID: i.cid, URL: i.url}
}
func (r *nativeRelay) offer(gid uint32, a, b relayIdentity, direct string) []string {
	if r == nil {
		return []string{direct}
	}
	return r.core.Offer(gid, a.shared(), b.shared(), direct)
}
func (r *nativeRelay) probeTarget(caller sessionOwner, url string) (sessionOwner, uint32, bool) {
	if r == nil {
		return sessionOwner{}, 0, false
	}
	target, gid, ok := r.core.ProbeTarget(relayOwner(caller), url)
	return sessionOwner{port: target.LocalPort, peer: target.PeerID, pid: target.PID}, gid, ok
}
func (r *nativeRelay) requestProbe(caller sessionOwner, url string) {
	if r != nil {
		r.core.RequestProbe(relayOwner(caller), url)
	}
}
func (r *nativeRelay) removeParticipant(owner sessionOwner, gid uint32) {
	if r != nil {
		r.core.RemoveParticipant(relayOwner(owner), gid)
	}
}
func (r *nativeRelay) drainInstructions() []quazal.ProbeInstruction {
	return r.core.DrainInstructions()
}
func (r *nativeRelay) Serve(ctx context.Context) error { return r.core.Serve(ctx) }
func (r *nativeRelay) Close() error                    { return r.core.Close() }
func stationField(url, key string) string              { return peerrelay.Field(url, key) }
func relayStationURL(original string, address netip.AddrPort, cid uint32) string {
	return peerrelay.StationURL(original, address, cid)
}
