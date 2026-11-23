package commandos

import "github.com/princebyyourlogic/quetzal/internal/protocol/quazal"

const gatheringType = "Commandos3DGameGathering"

// Native 0x45fb40/0x45fc00 serialize fields. The virtual Data wrapper adds a
// length prefix inside the typed Any buffer (confirmed by stock 21/1 traffic).
type gathering struct {
	quazal.Gathering
	Mode, PlayerCount byte
	FlagA, FlagB      bool
	Name              [32]byte
	Details           [512]byte
	Discriminator     uint32
}

func decodeGathering(args []byte) (gathering, error) {
	r := quazal.NewReader(args)
	any := r.Any(2048)
	if !r.Done() || any.Type != gatheringType {
		return gathering{}, quazal.ErrRMC
	}
	data := quazal.NewReader(any.Payload)
	fields := data.Buffer(2048)
	if !data.Done() {
		return gathering{}, quazal.ErrRMC
	}
	r = quazal.NewReader(fields)
	g := gathering{Gathering: r.Gathering()}
	g.Mode = r.U8()
	g.PlayerCount = r.U8()
	g.FlagA = r.Bool()
	g.FlagB = r.Bool()
	copy(g.Name[:], r.Raw(32))
	copy(g.Details[:], r.Raw(512))
	g.Discriminator = r.U32()
	if !r.Done() || g.MinParticipants > 8 || g.MaxParticipants > 8 || g.PlayerCount > 8 || g.Mode > 3 {
		return gathering{}, quazal.ErrRMC
	}
	return g, nil
}
func (g gathering) encode() ([]byte, error) {
	var body quazal.Writer
	body.Gathering(g.Gathering)
	body.U8(g.Mode)
	body.U8(g.PlayerCount)
	body.Bool(g.FlagA)
	body.Bool(g.FlagB)
	body.Raw(g.Name[:])
	body.Raw(g.Details[:])
	body.U32(g.Discriminator)
	b, err := body.Bytes()
	if err != nil {
		return nil, err
	}
	var data quazal.Writer
	data.Buffer(b)
	wrapped, err := data.Bytes()
	if err != nil {
		return nil, err
	}
	var w quazal.Writer
	w.Any(quazal.AnyData{Type: gatheringType, Payload: wrapped})
	return w.Bytes()
}

// Retail builds exactly these five-position anchored filters at 0x456080.
// Evaluate that small grammar directly instead of accepting arbitrary regex.
func descriptionFilter(pattern string) (func(string) bool, error) {
	if len(pattern) != 6 || pattern[0] != '^' {
		return nil, quazal.ErrRMC
	}
	allowed := []string{"LW", ".PA", ".DTS", ".FX", ".2"}
	for i, values := range allowed {
		valid := false
		for j := 0; j < len(values); j++ {
			valid = valid || pattern[i+1] == values[j]
		}
		if !valid {
			return nil, quazal.ErrRMC
		}
	}
	return func(description string) bool {
		if len(description) < 5 {
			return false
		}
		for i := 0; i < 5; i++ {
			if pattern[i+1] != '.' && pattern[i+1] != description[i] {
				return false
			}
		}
		return true
	}, nil
}
