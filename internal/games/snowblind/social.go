package snowblind

import "github.com/princebyyourlogic/quetzal/internal/protocol/quazal"

// clanNotMember is mapped to clan1_dialog_error_notinclan by native 0x1fa6d8.
const clanNotMember uint32 = 0x68

// Empty read replies used by startup instances without persistent social stores.
// Production adapters route through the tenant friends/clan stores instead.
// 0x160c40/0x8005 reads a friend list; 0x171f98/0x8010 reads a status u32
// followed by 0x173eb0's three bools and two Strings, even for no alerts.
func socialQuery(request quazal.Request) ([]byte, error) {
	r := quazal.NewReader(request.Args)
	var w quazal.Writer
	switch {
	case request.Protocol == 103 && request.Method == 5:
		r.String(2048)
		w.U32(0)
	case request.Protocol == 102 && request.Method == 2:
		// 0x171460/0x8002 always emits all five outputs, including on no membership.
		w.U32(clanNotMember)
		w.String("")
		w.String("")
		w.U32(0)
		w.U32(0) // ClanDesc: name/abbr, i32, notices.
		writeEmptyClanAlerts(&w)
		w.U32(0)                 // Member list.
		writeEmptyClanMember(&w) // Own member descriptor.
	case request.Protocol == 102 && request.Method == 16:
		w.U32(0)
		writeEmptyClanAlerts(&w)
	default:
		return nil, quazal.ErrRMC
	}
	if !r.Done() {
		return nil, quazal.ErrRMC
	}
	return w.Bytes()
}

// Native 0x173e48/0x173eb0 alert record.
func writeEmptyClanAlerts(w *quazal.Writer) {
	w.Bool(false)
	w.Bool(false)
	w.Bool(false)
	w.String("")
	w.String("")
}

// Native 0x173ba0/0x173ca0 member record: two Strings, three bools,
// a list of typed values, and a final bool. No membership grants no roles.
func writeEmptyClanMember(w *quazal.Writer) {
	w.String("")
	w.String("")
	w.Bool(false)
	w.Bool(false)
	w.Bool(false)
	w.U32(0)
	w.Bool(false)
}
