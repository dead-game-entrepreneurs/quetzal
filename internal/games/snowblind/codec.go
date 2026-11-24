package snowblind

import "github.com/princebyyourlogic/quetzal/internal/protocol/quazal"

// Native BO3Match fields: 0x15dd10/0x15dd88, with the shared Gathering base.
// The two adjacent option bytes are preserved as a little-endian modifier word.
type gathering struct {
	quazal.Gathering
	Kind           string
	Platform, Mode byte
	Mods           uint16
	Players        byte
	Map            uint32
}

func decodeGathering(args []byte) (gathering, error) {
	r := quazal.NewReader(args)
	any := r.Any(2048)
	if !r.Done() || (any.Type != "BO3Match" && any.Type != "Gathering") {
		return gathering{}, quazal.ErrRMC
	}
	d := quazal.NewReader(any.Payload)
	body := d.Buffer(2048)
	if !d.Done() {
		return gathering{}, quazal.ErrRMC
	}
	r = quazal.NewReader(body)
	g := gathering{Gathering: r.Gathering(), Kind: any.Type}
	if g.Kind == "BO3Match" {
		g.Platform = r.U8()
		g.Mode = r.U8()
		g.Mods = uint16(r.U8())
		g.Mods |= uint16(r.U8()) << 8
		g.Players = r.U8()
		g.Map = r.U32()
		if g.MaxParticipants == 0 || g.Players > byte(g.MaxParticipants) {
			return gathering{}, quazal.ErrRMC
		}
	} else if g.Description != "BO3Round" {
		return gathering{}, quazal.ErrRMC
	}
	if !r.Done() || g.MinParticipants > g.MaxParticipants || g.MaxParticipants > 32 {
		return gathering{}, quazal.ErrRMC
	}
	return g, nil
}
func (g gathering) encode() ([]byte, error) {
	var body quazal.Writer
	body.Gathering(g.Gathering)
	if g.Kind == "BO3Match" {
		body.U8(g.Platform)
		body.U8(g.Mode)
		body.U8(byte(g.Mods))
		body.U8(byte(g.Mods >> 8))
		body.U8(g.Players)
		body.U32(g.Map)
	}
	b, err := body.Bytes()
	if err != nil {
		return nil, err
	}
	var data quazal.Writer
	data.Buffer(b)
	b, err = data.Bytes()
	if err != nil {
		return nil, err
	}
	var w quazal.Writer
	w.Any(quazal.AnyData{Type: g.Kind, Payload: b})
	return w.Bytes()
}
