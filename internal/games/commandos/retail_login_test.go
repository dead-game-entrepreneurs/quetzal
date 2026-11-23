package commandos

import (
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// The 2026-11-23 US retail attempt sent 37 application bytes: selector 0,
// then a 32-byte RMC body (10/1, call 4, shipped signup-principal String).
// Reproduce only those public fields, without storing the user's capture.
func TestDefaultTransportAcceptsRetailBootstrapLogin(t *testing.T) {
	addresses := startAdapter(t, DefaultConfig().PayloadMode)
	auth := connect(t, addresses[0], nil, "plain")
	auth.call = 3
	reply := auth.ok(10, 1, args(func(w *quazal.Writer) { w.String(signupPrincipal) }))
	r := quazal.NewReader(reply)
	if r.U32() != quazal.Success || r.U32() != quazal.GuestPID {
		t.Fatal("retail-shaped bootstrap login rejected")
	}
}
