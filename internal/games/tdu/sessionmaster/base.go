package sessionmaster

const (
	GenericCreate      uint16 = 0x20db
	GenericCreateReply uint16 = 0xe72c
	GenericJoin        uint16 = 0xeb6b
	GenericJoinReply   uint16 = 0xd553
	GenericUpdate      uint16 = 0xe133
	BaseSessionTag     uint16 = 0x7eca
)

type SessionKind byte

const (
	FreeRideSession SessionKind = iota
	GenericSession
)

// BaseSession is edNetSessionV3, distinct from the FreeRideV3 object. Legacy
// fields are preserved at their full wire widths, without guessing game enums.
type BaseSession struct {
	Identity            uint64
	Reference           uint32
	Connection          [4]uint32
	Opaque              uint64
	NAT                 byte
	Game, Name          string
	Occupancy, Capacity byte
	Fields              [5]uint32
	ID                  uint32
	Extra               []byte
}

func (d BaseSession) validate() error {
	if !validString(d.Game, 32) || !validString(d.Name, 32) || d.Capacity < 1 || d.Capacity > 8 || d.Occupancy > d.Capacity || len(d.Extra) > 128 {
		return ErrMalformed
	}
	return nil
}
func (d BaseSession) MarshalBinary() ([]byte, error) {
	if e := d.validate(); e != nil {
		return nil, e
	}
	w := writer{}
	w.u16(BaseSessionTag)
	w.u64(d.Identity)
	w.u32(d.Reference)
	for _, v := range d.Connection {
		w.u32(v)
	}
	w.u64(d.Opaque)
	w.byte(d.NAT)
	w.blob([]byte(d.Game))
	w.blob([]byte(d.Name))
	w.byte(d.Occupancy)
	w.byte(d.Capacity)
	for _, v := range d.Fields {
		w.u32(v)
	}
	w.u32(d.ID)
	if len(d.Extra) > 0 {
		w.blob(d.Extra)
	}
	return w.b, nil
}
func ParseBaseSession(b []byte) (BaseSession, error) {
	r := reader{b: b}
	d := BaseSession{}
	if r.u16() != BaseSessionTag {
		return d, ErrMalformed
	}
	d.Identity = r.u64()
	d.Reference = r.u32()
	for i := range d.Connection {
		d.Connection[i] = r.u32()
	}
	d.Opaque = r.u64()
	d.NAT = r.byte()
	d.Game = string(r.blob(32))
	d.Name = string(r.blob(32))
	d.Occupancy = r.byte()
	d.Capacity = r.byte()
	for i := range d.Fields {
		d.Fields[i] = r.u32()
	}
	d.ID = r.u32()
	if r.pos < len(r.b) {
		d.Extra = append([]byte(nil), r.blob(128)...)
	}
	if !r.done() || d.validate() != nil {
		return BaseSession{}, ErrMalformed
	}
	return d, nil
}
func (d BaseSession) freeRide() FreeRide {
	return FreeRide{Identity: d.Identity, ID: d.ID, Reference: d.Reference, Connection: d.Connection, NAT: d.NAT, Game: d.Game, Name: d.Name, Occupancy: d.Occupancy, Capacity: d.Capacity}
}
func (r Snapshot) BaseSession() BaseSession {
	d := r.Descriptor
	b := BaseSession{Identity: d.Identity, ID: d.ID, Reference: d.Reference, Connection: d.Connection, NAT: d.NAT, Game: d.Game, Name: d.Name, Occupancy: d.Occupancy, Capacity: d.Capacity}
	if r.Base != nil {
		b.Opaque = r.Base.Opaque
		b.Fields = r.Base.Fields
		b.Extra = append([]byte(nil), r.Base.Extra...)
	}
	b.Fields[1] = uint32(r.Restrictions.Opaque70)
	b.Fields[2] = uint32(int32(r.Restrictions.Signed))
	b.Fields[3] = uint32(r.Restrictions.ID)
	b.Fields[4] = uint32(r.Restrictions.Category)
	return b
}

type GenericRequest struct {
	Client    Client
	Session   BaseSession
	Blacklist []Exclusion
	Mode      uint32
}

func ParseGenericRequest(b []byte) (GenericRequest, error) {
	r := reader{b: b}
	q := GenericRequest{}
	code := r.u16()
	if code != GenericCreate && code != GenericJoin {
		return q, ErrMalformed
	}
	var e error
	q.Client, e = ParseClient(r.blob(256))
	if e != nil {
		return q, e
	}
	q.Session, e = ParseBaseSession(r.blob(1024))
	if e != nil {
		return q, e
	}
	if code == GenericJoin {
		bl := reader{b: r.blob(130)}
		if bl.u16() != BlacklistTag {
			return q, ErrMalformed
		}
		for bl.pos < len(bl.b) && bl.err == nil {
			if len(q.Blacklist) == 16 {
				return q, ErrMalformed
			}
			x := Exclusion{bl.u32(), bl.f32()}
			if !finite(x.Seconds) || x.Seconds < 0 || x.Seconds > 86400 {
				return q, ErrMalformed
			}
			q.Blacklist = append(q.Blacklist, x)
		}
		if !bl.done() {
			return q, ErrMalformed
		}
		if r.pos < len(r.b) {
			q.Mode = r.u32()
		}
	}
	if !r.done() {
		return GenericRequest{}, ErrMalformed
	}
	return q, nil
}
func (q GenericRequest) MarshalBinary(code uint16) ([]byte, error) {
	if code != GenericCreate && code != GenericJoin {
		return nil, ErrMalformed
	}
	c, e := q.Client.MarshalBinary()
	if e != nil {
		return nil, e
	}
	d, e := q.Session.MarshalBinary()
	if e != nil {
		return nil, e
	}
	w := writer{}
	w.u16(code)
	w.blob(c)
	w.blob(d)
	if code == GenericJoin {
		bl := writer{}
		bl.u16(BlacklistTag)
		if len(q.Blacklist) > 16 {
			return nil, ErrMalformed
		}
		for _, x := range q.Blacklist {
			if !finite(x.Seconds) || x.Seconds < 0 || x.Seconds > 86400 {
				return nil, ErrMalformed
			}
			bl.u32(x.ID)
			bl.f32(x.Seconds)
		}
		w.blob(bl.b)
		w.u32(q.Mode)
	}
	return w.b, nil
}
func MarshalGenericUpdate(d BaseSession) ([]byte, error) {
	b, e := d.MarshalBinary()
	if e != nil {
		return nil, e
	}
	w := writer{}
	w.u16(GenericUpdate)
	w.blob(b)
	return w.b, nil
}
func marshalGenericCreated(r Snapshot) ([]byte, error) {
	b, e := r.BaseSession().MarshalBinary()
	if e != nil {
		return nil, e
	}
	w := writer{}
	w.u16(GenericCreateReply)
	w.byte(1)
	w.u32(r.ID)
	w.blob(b)
	return w.b, nil
}
func marshalGenericJoined(r *Snapshot) ([]byte, error) {
	w := writer{}
	w.u16(GenericJoinReply)
	if r == nil {
		w.byte(0)
		w.u32(0)
		return w.b, nil
	}
	b, e := r.Descriptor.MarshalBinary()
	if e != nil {
		return nil, e
	}
	w.byte(1)
	w.u32(r.ID)
	w.blob(b)
	// 1359d0 serializes the stored source endpoint as edNetClientV2. Type2
	// contains observed IPv4/port and no type0 key/string or optional extension.
	endpoint := writer{}
	endpoint.u16(0x269e)
	endpoint.u32(ipv4(r.Owner.Remote.Addr()))
	endpoint.u16(r.Owner.Remote.Port())
	endpoint.u16(2)
	w.blob(endpoint.b)
	return w.b, nil
}
