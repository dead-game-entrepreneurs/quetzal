package sessionmaster

const (
	SessionDensity      uint16 = 0x510b
	SessionDensityReply uint16 = 0x1454
	// US stock CPSpots contains 210 island entries plus 238 mission entries.
	// The client appends its type-6 Drive-In aggregate after these 448 entries.
	stockAggregateIndex = 448
	stockDensityCells   = stockAggregateIndex + 1
)

// SessionDensity classifies only proven names. A private stock-catalog mapping
// enables ordinary activity cells; DriveIn_PS2 always uses the type6 aggregate.
func (s *Service) SessionDensity() []byte {
	cells := make([]byte, stockDensityCells)
	var total uint32
	for _, r := range s.store.Snapshot() {
		if r.Kind != GenericSession || !r.Published {
			continue
		}
		index, ok := s.opts.ActivityCatalog[r.Descriptor.Name]
		if r.Descriptor.Name == "DriveIn_PS2" {
			index = stockAggregateIndex
			ok = true
		}
		if !ok {
			continue
		}
		total++
		if cells[index] < 255 {
			cells[index]++
		}
	}
	w := writer{}
	w.u16(SessionDensityReply)
	w.u32(stockDensityCells)
	w.u32(total)
	if total > 0 {
		w.blob(cells)
	}
	return w.b
}
