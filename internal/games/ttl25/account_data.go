package ttl25

import "github.com/princebyyourlogic/quetzal/internal/protocol/quazal"

// ValidateAccountData retains the title API for the shared Eidos profile codec.
func ValidateAccountData(public, private quazal.AnyData) error {
	return quazal.ValidateEidosAccountData(public, private)
}
